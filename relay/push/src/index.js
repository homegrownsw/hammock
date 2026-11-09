// Hammock's push relay (ADR-0007): the only place that holds the APNs key.
//
// A phone registers its APNs device token and gets back a random id and a
// secret. It gives your Mac the id (never the token) and its own encryption
// key; the Mac sends notifications for it here as ciphertext, which this
// relay passes to Apple untouched. The phone's notification extension
// decrypts it.
//
// What the relay sees: a phone's APNs device token and whether it's a
// development build; and for each send, the phone's id, the ciphertext,
// when it's sent and from which address, and three hints for Apple: a
// thread and a collapse tag (keyed hashes of a pane's or a request's id,
// not the ids: the same for the same thing, meaningless otherwise) and
// the category ("join", "grant" or "hammockkey": what kind of
// notification it is). Nothing of what it says.
//
// Routes (JSON in and out):
//   POST /register   {token, sandbox, id?, secret?} → {id, secret} (the
//                    same id, given its secret: a phone's new token)
//   POST /unregister {id, secret}           → {}
//   POST /send       {id, payload, thread?, collapse?, category?} → {}
//
// Secrets (wrangler secret put): APNS_KEY (the .p8 file's contents),
// APNS_KEY_ID, APNS_TEAM_ID. Vars: APNS_TOPIC (the app's bundle id).
// Bindings: DB (D1, migrations/), REQUESTS (a rate limiter,
// per client address), SENDS (per device), REGISTERS (per client address).
// A daily cron trigger forgets devices nothing has sent to for 60 days.
//
// On the free plan every request counts, so whatever can be turned away
// is, before the database: other paths and methods, big bodies, too many
// requests from one address (an IPv6 address counts by its /64, which one
// customer holds), malformed ids and payloads, and too many sends to one
// id. A send is then one row read (and a write at most once a day, keeping
// the device's last use); only registering and forgetting write otherwise.
// That doesn't stop a crowd of addresses: in production, put a Cloudflare
// WAF rate-limiting rule in front too (README.md).

const MAX_PAYLOAD = 3000 // base64 ciphertext; APNs allows 4 KB in all
const MAX_BODY = 4096 // the payload and the rest of a send
const ROUTES = new Set(['/register', '/unregister', '/send'])
const CATEGORIES = new Set(['join', 'grant', 'hammockkey'])
const DAY = 24 * 60 * 60 * 1000
const FORGET_AFTER = 60 * DAY // unused for this long: forgotten (scheduled)

export default {
  async fetch(request, env) {
    const url = new URL(request.url)
    if (!ROUTES.has(url.pathname)) return json({ error: 'not found' }, 404)
    if (request.method !== 'POST') return json({ error: 'POST only' }, 405)
    const client = clientKey(request.headers.get('cf-connecting-ip') || '')
    if (env.REQUESTS && !(await env.REQUESTS.limit({ key: client })).success) return json({ error: 'slow down' }, 429)
    if (Number(request.headers.get('content-length') || 0) > MAX_BODY) return json({ error: 'too big' }, 413)
    let body
    try {
      const text = await request.text()
      if (text.length > MAX_BODY) return json({ error: 'too big' }, 413)
      body = JSON.parse(text)
    } catch {
      return json({ error: 'bad JSON' }, 400)
    }
    if (!body || typeof body !== 'object') return json({ error: 'bad JSON' }, 400)
    try {
      switch (url.pathname) {
        case '/register':
          return await register(body, env, client)
        case '/unregister':
          return await unregister(body, env)
        case '/send':
          return await send(body, env)
      }
      return json({ error: 'not found' }, 404)
    } catch (e) {
      // Details in the worker's log (wrangler tail), not to whoever asked.
      console.error('relay', url.pathname, e)
      return json({ error: 'internal error' }, 500)
    }
  },

  // The cron trigger (wrangler.toml): forget devices unused for 60 days,
  // so made-up registrations don't pile up.
  async scheduled(_event, env) {
    await forgetUnused(env)
  },
}

export async function forgetUnused(env, now = Date.now()) {
  await env.DB.prepare('DELETE FROM devices WHERE last_used < ?').bind(sqlTime(now - FORGET_AFTER)).run()
}

// What a client's requests are counted by: its address, or for IPv6 its
// /64 (one customer's whole network: a single address would let anyone
// with one go round the limit by changing it).
export function clientKey(ip) {
  ip = String(ip).trim().toLowerCase()
  if (!ip.includes(':')) return 'ip:' + ip
  const [head, tail] = ip.includes('::') ? ip.split('::', 2) : [ip, null]
  const h = head ? head.split(':') : []
  const t = tail ? tail.split(':') : []
  // An IPv4 address in IPv6 form (::ffff:192.0.2.1): that address.
  const last = (t.length ? t : h).at(-1) || ''
  if (last.includes('.')) return 'ip:' + last
  const groups = [...h, ...(tail === null ? [] : Array(Math.max(0, 8 - h.length - t.length)).fill('0')), ...t]
  return 'v6:' + groups.slice(0, 4).map((g) => (parseInt(g, 16) || 0).toString(16)).join(':') + '::/64'
}

async function register({ token, sandbox, id, secret }, env, client) {
  if (typeof token !== 'string' || !/^[0-9a-f]{64,200}$/i.test(token)) return json({ error: 'bad token' }, 400)
  // A phone registers once per token, so a few a minute from one address is
  // plenty: no one can fill the table from a script.
  if (env.REGISTERS && !(await env.REGISTERS.limit({ key: client })).success) return json({ error: 'slow down' }, 429)
  // A phone that has an id already (a new token, or reinstalled) keeps it,
  // proving it's that phone with the id's secret.
  if (validID(id) && typeof secret === 'string') {
    const dev = await device(env, id)
    if (dev && dev.secret === (await sha256(secret))) {
      await env.DB.prepare('UPDATE devices SET token = ?, sandbox = ?, last_used = ? WHERE id = ?').bind(token, sandbox ? 1 : 0, sqlTime(Date.now()), id).run()
      return json({ id, secret })
    }
  }
  // Otherwise a new id. Any earlier one for the same token stays: only its
  // secret could replace it (whoever learnt a phone's token mustn't be able
  // to cut it off), and if nothing sends to it, it's forgotten in 60 days.
  const newID = randomHex(16)
  const newSecret = randomHex(24)
  await env.DB.prepare('INSERT INTO devices (id, token, sandbox, secret, last_used) VALUES (?, ?, ?, ?, ?)')
    .bind(newID, token, sandbox ? 1 : 0, await sha256(newSecret), sqlTime(Date.now()))
    .run()
  return json({ id: newID, secret: newSecret })
}

async function unregister({ id, secret }, env) {
  if (!validID(id)) return json({ error: 'unknown device' }, 404)
  const dev = await device(env, id)
  if (!dev || dev.secret !== (await sha256(String(secret)))) return json({ error: 'unknown device' }, 404)
  await forget(env, id)
  return json({})
}

async function send({ id, payload, thread, collapse, category }, env) {
  // Everything that needs no database first.
  if (!validID(id)) return json({ error: 'unknown device' }, 404)
  if (typeof payload !== 'string' || payload.length > MAX_PAYLOAD || !/^[A-Za-z0-9+/=]+$/.test(payload)) {
    return json({ error: 'bad payload' }, 400)
  }
  // At most 60 a minute per id (approximate): a leaked id can't flood a
  // phone, nor a made-up one cost more than that in reads.
  if (env.SENDS && !(await env.SENDS.limit({ key: id })).success) return json({ error: 'slow down' }, 429)
  const dev = await device(env, id)
  if (!dev) return json({ error: 'unknown device' }, 404)
  const aps = {
    // Shown only if the phone can't decrypt (the extension replaces it).
    alert: { title: 'Hammock', body: 'Something needs you' },
    'mutable-content': 1,
    sound: 'NeedsYou.caf', // Hammock's kalimba note, in the app's bundle (HammockNotify sets it too)
  }
  // Only Hammock's own categories (the phone sets it again from the note).
  if (CATEGORIES.has(category)) aps.category = category
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
  if (!r.ok) {
    // APNs' reason goes to the log, not the caller: 410 tells a Mac the
    // phone's gone (it stops sending to it), anything else is a refusal.
    const text = await r.text().catch(() => '')
    let reason
    try {
      reason = JSON.parse(text).reason
    } catch {}
    console.error('apns', r.status, text.slice(0, 200))
    if (r.status === 410 || reason === 'BadDeviceToken') await forget(env, id) // the app was removed
    if (r.status === 410 || reason === 'BadDeviceToken' || reason === 'DeviceTokenNotForTopic') return json({ error: 'gone' }, 410)
    return json({ error: 'refused' }, 502)
  }
  // Still in use: kept (written at most once a day).
  if (!(Date.parse(dev.lastUsed.replace(' ', 'T') + 'Z') > Date.now() - DAY)) {
    await env.DB.prepare('UPDATE devices SET last_used = ? WHERE id = ?').bind(sqlTime(Date.now()), id).run()
  }
  return json({})
}

function validID(id) {
  return typeof id === 'string' && /^[0-9a-f]{32}$/.test(id)
}

async function device(env, id) {
  if (!validID(id)) return null
  const row = await env.DB.prepare('SELECT token, sandbox, secret, last_used FROM devices WHERE id = ?').bind(id).first()
  return row ? { token: row.token, sandbox: !!row.sandbox, secret: row.secret, lastUsed: String(row.last_used || '') } : null
}

// SQLite's datetime() form, in UTC: "2026-11-09 12:34:56".
function sqlTime(ms) {
  return new Date(ms).toISOString().slice(0, 19).replace('T', ' ')
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
