// Hammock's push relay (ADR-0007): the only place that holds the APNs key.
//
// A phone registers its APNs device token and gets back a random id and a
// secret. It gives your Mac the id (never the token) and its own encryption
// key; the Mac sends notifications for it here as ciphertext, which this
// relay passes to Apple untouched. The phone's notification extension
// decrypts it. The relay sees device tokens and opaque bytes, nothing else.
//
// Routes (JSON in and out):
//   POST /register   {token, sandbox}       → {id, secret} (replacing any
//                                             earlier id for that token)
//   POST /unregister {id, secret}           → {}
//   POST /send       {id, payload, thread?, collapse?, category?} → {}
//
// Secrets (wrangler secret put): APNS_KEY (the .p8 file's contents),
// APNS_KEY_ID, APNS_TEAM_ID. Vars: APNS_TOPIC (the app's bundle id).
// Bindings: DB (D1, schema.sql), SENDS (a rate limiter, per device),
// REGISTERS (a rate limiter, per client IP).
//
// On the free plan a send is one request and one row read; only
// registering and forgetting write.

const MAX_PAYLOAD = 3000 // base64 ciphertext; APNs allows 4 KB in all

export default {
  async fetch(request, env) {
    const url = new URL(request.url)
    if (request.method !== 'POST') return json({ error: 'POST only' }, 405)
    let body
    try {
      body = await request.json()
    } catch {
      return json({ error: 'bad JSON' }, 400)
    }
    try {
      switch (url.pathname) {
        case '/register':
          return await register(body, env, request.headers.get('cf-connecting-ip') || '')
        case '/unregister':
          return await unregister(body, env)
        case '/send':
          return await send(body, env)
      }
      return json({ error: 'not found' }, 404)
    } catch (e) {
      return json({ error: String(e.message || e) }, 500)
    }
  },
}

async function register({ token, sandbox }, env, ip) {
  if (typeof token !== 'string' || !/^[0-9a-f]{64,200}$/i.test(token)) return json({ error: 'bad token' }, 400)
  // A phone registers once per token, so a few a minute from one address is
  // plenty: no one can fill the table from a script.
  if (env.REGISTERS && !(await env.REGISTERS.limit({ key: 'ip:' + ip })).success) return json({ error: 'slow down' }, 429)
  const id = randomHex(16)
  const secret = randomHex(24)
  // One row per device token: registering again (a reinstalled app)
  // replaces the earlier id instead of adding another.
  await env.DB.batch([
    env.DB.prepare('DELETE FROM devices WHERE token = ?').bind(token),
    env.DB.prepare('INSERT INTO devices (id, token, sandbox, secret) VALUES (?, ?, ?, ?)').bind(id, token, sandbox ? 1 : 0, await sha256(secret)),
  ])
  return json({ id, secret })
}

async function unregister({ id, secret }, env) {
  const dev = await device(env, id)
  if (!dev || dev.secret !== (await sha256(String(secret)))) return json({ error: 'unknown device' }, 404)
  await forget(env, id)
  return json({})
}

async function send({ id, payload, thread, collapse, category }, env) {
  const dev = await device(env, id)
  if (!dev) return json({ error: 'unknown device' }, 404)
  if (typeof payload !== 'string' || payload.length > MAX_PAYLOAD || !/^[A-Za-z0-9+/=]+$/.test(payload)) {
    return json({ error: 'bad payload' }, 400)
  }
  // At most 60 a minute per phone (approximate): a leaked id can't flood one.
  if (env.SENDS && !(await env.SENDS.limit({ key: id })).success) return json({ error: 'slow down' }, 429)
  const aps = {
    // Shown only if the phone can't decrypt (the extension replaces it).
    alert: { title: 'Hammock', body: 'Something needs you' },
    'mutable-content': 1,
    sound: 'NeedsYou.caf', // Hammock's kalimba note, in the app's bundle (HammockNotify sets it too)
  }
  if (category) aps.category = String(category).slice(0, 64)
  // A Hammock Key request waits on you for a couple of minutes: through Focus.
  if (category === 'hammockkey') aps['interruption-level'] = 'time-sensitive'
  if (thread) aps['thread-id'] = String(thread).slice(0, 64)
  const headers = {
    authorization: 'bearer ' + (await apnsJWT(env)),
    'apns-topic': env.APNS_TOPIC,
    'apns-push-type': 'alert',
    'apns-priority': '10',
  }
  if (collapse) headers['apns-collapse-id'] = String(collapse).slice(0, 64)
  const host = dev.sandbox ? 'https://api.sandbox.push.apple.com' : 'https://api.push.apple.com'
  const r = await fetch(`${host}/3/device/${dev.token}`, { method: 'POST', headers, body: JSON.stringify({ aps, e: payload }) })
  if (r.status === 410 || r.status === 400) {
    const reason = (await r.json().catch(() => ({}))).reason
    if (r.status === 410 || reason === 'BadDeviceToken') await forget(env, id) // the app was removed
    return json({ error: reason || 'gone' }, 410)
  }
  if (!r.ok) return json({ error: 'apns ' + r.status + ' ' + (await r.text()) }, 502)
  return json({})
}

async function device(env, id) {
  if (typeof id !== 'string' || !/^[0-9a-f]{32}$/.test(id)) return null
  const row = await env.DB.prepare('SELECT token, sandbox, secret FROM devices WHERE id = ?').bind(id).first()
  return row ? { token: row.token, sandbox: !!row.sandbox, secret: row.secret } : null
}

async function forget(env, id) {
  await env.DB.prepare('DELETE FROM devices WHERE id = ?').bind(id).run()
}

// ---------- APNs auth: an ES256 JWT, reused for up to 50 minutes ----------

let cached = { jwt: '', at: 0 }

export async function apnsJWT(env, now = Date.now()) {
  if (cached.jwt && now - cached.at < 50 * 60 * 1000) return cached.jwt
  const header = b64url(JSON.stringify({ alg: 'ES256', kid: env.APNS_KEY_ID }))
  const claims = b64url(JSON.stringify({ iss: env.APNS_TEAM_ID, iat: Math.floor(now / 1000) }))
  const key = await crypto.subtle.importKey('pkcs8', pemToDer(env.APNS_KEY), { name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign'])
  const sig = await crypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, key, new TextEncoder().encode(header + '.' + claims))
  cached = { jwt: header + '.' + claims + '.' + b64url(new Uint8Array(sig)), at: now }
  return cached.jwt
}

export function resetJWT() {
  cached = { jwt: '', at: 0 }
}

function pemToDer(pem) {
  const b64 = String(pem).replace(/-----[^-]+-----/g, '').replace(/\s+/g, '')
  return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
}

function b64url(data) {
  const bytes = typeof data === 'string' ? new TextEncoder().encode(data) : data
  let s = ''
  for (const b of bytes) s += String.fromCharCode(b)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function randomHex(n) {
  return [...crypto.getRandomValues(new Uint8Array(n))].map((b) => b.toString(16).padStart(2, '0')).join('')
}

async function sha256(s) {
  const d = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(s))
  return [...new Uint8Array(d)].map((b) => b.toString(16).padStart(2, '0')).join('')
}

function json(v, status = 200) {
  return new Response(JSON.stringify(v), { status, headers: { 'content-type': 'application/json' } })
}
