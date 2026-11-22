// Hammock's push relay (ADR-0007): the only place that holds the APNs key
// (and Firebase's service account, for Android phones).
//
// A phone registers its device token (APNs', or an FCM registration token)
// and gets back a random id and a secret. It gives your Mac the id (never
// the token) and its own encryption key; the Mac sends notifications for it
// here as ciphertext, which this relay passes to Apple or Google untouched.
// The phone (the iPhone's notification extension, the Android app's
// messaging service) decrypts it.
//
// What the relay sees: a phone's device token, which service it's for, and
// (an iPhone's) whether it's a development build; and for each send, the
// phone's id, the ciphertext, when it's sent and from which address, and
// three hints for Apple or Google: a thread and a collapse tag (keyed hashes
// of a pane's or a request's id, not the ids: the same for the same thing,
// meaningless otherwise) and the category ("join", "grant" or "hammockkey":
// what kind of notification it is). Nothing of what it says.
//
// Except a Live Activity's (an iPhone's, when it has Live Activities on:
// they're off unless you turn them on). Apple draws one from its content
// itself, so that comes in plain text, and the relay sees it as it passes:
// an agent's name, its Group and tab, and what it's doing or asking. It
// goes to the activity's own token (push-to-start's, or an update's),
// which the Mac sends with it; the relay keeps neither.
//
// Routes (JSON in and out):
//   POST /register   {token, sandbox, provider?, id?, secret?} → {id, secret}
//                    (provider "apns", the default, or "fcm"; the same id,
//                    given its secret, while the relay has it: a phone's
//                    new token or its weekly refresh. A forgotten id, or
//                    one the relay never made, gets a new one)
//   POST /unregister {id, secret}           → {}
//   POST /send       {id, payload, thread?, collapse?, category?} → {}
//                    {id, live: {token, event, state, attributes?, alert?,
//                    stale?, dismiss?, priority?}} → {} (an iPhone's Live
//                    Activity: event "start" (with attributes and alert),
//                    "update" or "end"; 410 if Apple no longer knows the
//                    token, which leaves the phone registered)
//   POST /clear      {id, payload}           → {} (a silent push: what a
//                    notification was about is dealt with, so the phone
//                    takes it down; it says which only in the ciphertext)
//
// Secrets (wrangler secret put): APNS_KEY (the .p8 file's contents),
// APNS_KEY_ID, APNS_TEAM_ID; FCM_SERVICE_ACCOUNT (a Firebase service
// account's JSON key; its project_id is the FCM project). Vars: APNS_TOPIC
// (the app's bundle id).
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
const ROUTES = new Set(['/register', '/unregister', '/send', '/clear'])
const CATEGORIES = new Set(['join', 'grant', 'hammockkey'])
const DAY = 24 * 60 * 60 * 1000
const FORGET_AFTER = 60 * DAY // unused for this long: forgotten (scheduled)
const APNS_TOKEN = /^[0-9a-f]{64,200}$/i
const FCM_TOKEN = /^[A-Za-z0-9:_-]{20,1024}$/ // FCM registration tokens are ~160 characters
const FCM_TTL = '3600s' // the phone shows nothing sent over an hour ago anyway
const LIVE_EVENTS = new Set(['start', 'update', 'end'])
const LIVE_TOKEN = /^[0-9a-f]{32,400}$/i
const LIVE_ATTRIBUTES_TYPE = 'HammockActivityAttributes' // the app's ActivityAttributes (HammockKit's LiveActivity.swift)

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
        case '/clear':
          return await send(body, env, true)
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
  // A row written by the worker from before last_used (between migration
  // 0001 and the deploy that sets it) has none: counted from its creation.
  const before = sqlTime(now - FORGET_AFTER)
  await env.DB.prepare('DELETE FROM devices WHERE last_used < ? OR (last_used IS NULL AND created < ?)').bind(before, before).run()
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

async function register({ token, sandbox, provider, id, secret }, env, client) {
  provider ??= 'apns'
  if (provider !== 'apns' && provider !== 'fcm') return json({ error: 'bad provider' }, 400)
  if (typeof token !== 'string' || !(provider === 'fcm' ? FCM_TOKEN : APNS_TOKEN).test(token)) return json({ error: 'bad token' }, 400)
  // A relay without Firebase's key can't send to an Android phone: no row for one.
  if (provider === 'fcm' && !env.FCM_SERVICE_ACCOUNT) return json({ error: 'fcm unavailable' }, 501)
  // The sandbox is APNs': an Android phone's is always 0.
  sandbox = provider === 'apns' && sandbox ? 1 : 0
  // A phone registers once per token, so a few a minute from one address is
  // plenty: no one can fill the table from a script.
  if (env.REGISTERS && !(await env.REGISTERS.limit({ key: client })).success) return json({ error: 'slow down' }, 429)
  // A phone that has an id already (a new token, or reinstalled) keeps it,
  // proving it's that phone with the id's secret, while its row is here.
  if (validID(id) && typeof secret === 'string') {
    const dev = await device(env, id)
    if (dev && dev.secret === (await sha256(secret))) {
      await env.DB.prepare('UPDATE devices SET token = ?, sandbox = ?, provider = ?, last_used = ? WHERE id = ?').bind(token, sandbox, provider, sqlTime(Date.now()), id).run()
      return json({ id, secret })
    }
  }
  // Otherwise a new id, always made here: an id is never taken from the
  // client, so one forgotten (unused for 60 days, its token gone, or
  // unregistered) never comes back. It couldn't safely: every Mac the phone
  // ever paired with knows its id, unpaired ones too, and one bringing it
  // back with its own token would get the other Macs' pushes, which it can
  // decrypt (it has the phone's key). A forgotten phone gets a new id here
  // and tells each Mac when it next connects to it. Any earlier id for the
  // same token stays: only its secret could replace it (whoever learnt a
  // phone's token mustn't be able to cut it off), and if nothing sends to
  // it, it's forgotten in 60 days.
  const newID = randomHex(16)
  const newSecret = randomHex(24)
  await env.DB.prepare('INSERT INTO devices (id, token, sandbox, secret, last_used, provider) VALUES (?, ?, ?, ?, ?, ?)')
    .bind(newID, token, sandbox, await sha256(newSecret), sqlTime(Date.now()), provider)
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

async function send({ id, payload, thread, collapse, category, live }, env, clear = false) {
  // Everything that needs no database first.
  if (!validID(id)) return json({ error: 'unknown device' }, 404)
  if (live !== undefined && !clear) return sendLive(id, live, env)
  if (typeof payload !== 'string' || payload.length > MAX_PAYLOAD || !/^[A-Za-z0-9+/=]+$/.test(payload)) {
    return json({ error: 'bad payload' }, 400)
  }
  // At most 60 a minute per id (approximate): a leaked id can't flood a
  // phone, nor a made-up one cost more than that in reads.
  if (env.SENDS && !(await env.SENDS.limit({ key: id })).success) return json({ error: 'slow down' }, 429)
  const dev = await device(env, id)
  if (!dev) return json({ error: 'unknown device' }, 404)
  const result = dev.provider === 'fcm' ? await fcmSend(dev, { payload, thread, collapse, category }, env, clear) : await apnsSend(dev, { payload, thread, collapse, category }, env, clear)
  if (result === 'gone') {
    await forget(env, id)
    return json({ error: 'gone' }, 410)
  }
  if (result !== 'ok') return json({ error: 'refused' }, 502)
  // Still in use: kept (written at most once a day).
  if (!(Date.parse(dev.lastUsed.replace(' ', 'T') + 'Z') > Date.now() - DAY)) {
    await env.DB.prepare('UPDATE devices SET last_used = ? WHERE id = ?').bind(sqlTime(Date.now()), id).run()
  }
  return json({})
}

// A Live Activity push (see the top): checked, then to Apple with the
// activity's own token. Apple's turning the token down (the activity's
// gone, or a new push-to-start token since) is 410, and the phone stays.
async function sendLive(id, live, env) {
  if (!live || typeof live !== 'object' || Array.isArray(live)) return json({ error: 'bad live' }, 400)
  const { token, event, state, attributes, alert, stale, dismiss, priority } = live
  if (typeof token !== 'string' || !LIVE_TOKEN.test(token) || !LIVE_EVENTS.has(event) || !isObject(state)) return json({ error: 'bad live' }, 400)
  if (event === 'start' && (!isObject(attributes) || !isObject(alert))) return json({ error: 'bad live' }, 400)
  if (env.SENDS && !(await env.SENDS.limit({ key: id })).success) return json({ error: 'slow down' }, 429)
  const dev = await device(env, id)
  if (!dev) return json({ error: 'unknown device' }, 404)
  if (dev.provider !== 'apns') return json({ error: 'bad live' }, 400) // Android's is in its encrypted pushes
  const aps = { timestamp: Math.floor(Date.now() / 1000), event, 'content-state': state }
  if (event === 'start') {
    aps['attributes-type'] = LIVE_ATTRIBUTES_TYPE
    aps.attributes = attributes
    aps.alert = { title: String(alert.title ?? '').slice(0, 100), body: String(alert.body ?? '').slice(0, 200) }
  }
  if (Number.isInteger(stale) && stale > 0) aps['stale-date'] = stale
  if (event === 'end' && Number.isInteger(dismiss) && dismiss > 0) aps['dismissal-date'] = dismiss
  const headers = {
    authorization: 'bearer ' + (await apnsJWT(env)),
    'apns-topic': env.APNS_TOPIC + '.push-type.liveactivity',
    'apns-push-type': 'liveactivity',
    'apns-priority': priority === 10 ? '10' : '5',
  }
  const host = dev.sandbox ? 'https://api.sandbox.push.apple.com' : 'https://api.push.apple.com'
  const r = await fetch(`${host}/3/device/${token}`, { method: 'POST', headers, body: JSON.stringify({ aps }) })
  if (r.ok) return json({})
  const text = await r.text().catch(() => '')
  let reason
  try {
    reason = JSON.parse(text).reason
  } catch {}
  console.error('apns live', r.status, text.slice(0, 200))
  if (r.status === 410 || reason === 'BadDeviceToken' || reason === 'ExpiredToken') return json({ error: 'gone' }, 410)
  return json({ error: 'refused' }, 502)
}

function isObject(v) {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}

// One push through APNs: 'ok', 'gone' (forget the phone) or 'refused'.
async function apnsSend(dev, { payload, thread, collapse, category }, env, clear) {
  let aps, headers
  if (clear) {
    // Nothing shown: it wakes the app, which decrypts it and takes down the
    // notification it names. Background pushes go at low priority (APNs
    // turns down a 10), and iOS may hold them back.
    aps = { 'content-available': 1 }
    headers = {
      authorization: 'bearer ' + (await apnsJWT(env)),
      'apns-topic': env.APNS_TOPIC,
      'apns-push-type': 'background',
      'apns-priority': '5',
    }
  } else {
    aps = {
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
    headers = {
      authorization: 'bearer ' + (await apnsJWT(env)),
      'apns-topic': env.APNS_TOPIC,
      'apns-push-type': 'alert',
      'apns-priority': '10',
    }
    if (collapse) headers['apns-collapse-id'] = String(collapse).slice(0, 64)
  }
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
    // The app was removed, or the token is no good for it (another app's,
    // or the other APNs environment's): forgotten, and the Mac told it's gone.
    if (r.status === 410 || reason === 'BadDeviceToken' || reason === 'DeviceTokenNotForTopic') return 'gone'
    return 'refused'
  }
  return 'ok'
}

// One push through FCM (HTTP v1): 'ok', 'gone' (forget the phone) or
// 'refused'. Always a data-only message: the Android app decrypts it and
// builds the notification itself, so nothing is shown that it can't read.
async function fcmSend(dev, { payload, thread, collapse, category }, env, clear) {
  const sa = serviceAccount(env)
  let data, android
  if (clear) {
    // Like APNs' background push: only the ciphertext, which names what to take down.
    data = { clear: '1', e: payload }
    android = { priority: 'normal', ttl: FCM_TTL }
  } else {
    data = { e: payload }
    // Only Hammock's own categories (the phone sets it again from the note).
    if (CATEGORIES.has(category)) data.category = category
    if (thread) data.thread = String(thread).slice(0, 64)
    if (collapse) data.collapse = String(collapse).slice(0, 64)
    android = { priority: 'high', ttl: FCM_TTL }
    if (collapse) android.collapse_key = String(collapse).slice(0, 64)
  }
  const r = await fetch(`https://fcm.googleapis.com/v1/projects/${encodeURIComponent(sa.project_id)}/messages:send`, {
    method: 'POST',
    headers: { authorization: 'Bearer ' + (await fcmAccessToken(env)), 'content-type': 'application/json' },
    body: JSON.stringify({ message: { token: dev.token, data, android } }),
  })
  if (r.ok) return 'ok'
  // FCM's reason goes to the log, not the caller, as APNs' does.
  const text = await r.text().catch(() => '')
  let error = {}
  try {
    error = JSON.parse(text).error || {}
  } catch {}
  const code = (error.details || []).map((d) => d && d.errorCode).find(Boolean) || error.status || ''
  console.error('fcm', r.status, text.slice(0, 200))
  // Our access token was turned down: get a new one next time.
  if (r.status === 401) resetFCMToken()
  // The app was removed or the token expired (UNREGISTERED), the token is
  // malformed (INVALID_ARGUMENT about the token), or it's another Firebase
  // project's (SENDER_ID_MISMATCH): forgotten, and the Mac told it's gone.
  if (code === 'UNREGISTERED' || code === 'SENDER_ID_MISMATCH') return 'gone'
  if (code === 'INVALID_ARGUMENT' && /token/i.test(String(error.message || ''))) return 'gone'
  return 'refused'
}

function validID(id) {
  return typeof id === 'string' && /^[0-9a-f]{32}$/.test(id)
}

async function device(env, id) {
  if (!validID(id)) return null
  const row = await env.DB.prepare('SELECT token, sandbox, secret, last_used, provider FROM devices WHERE id = ?').bind(id).first()
  return row ? { token: row.token, sandbox: !!row.sandbox, secret: row.secret, lastUsed: String(row.last_used || ''), provider: row.provider || 'apns' } : null
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

// ---------- FCM auth: a service account's OAuth2 access token ----------
//
// An RS256 JWT signed with the service account's key, exchanged at Google's
// token endpoint for an access token (an hour's), reused until a minute
// before it expires. Isolates are short-lived, so this is per isolate.

const GOOGLE_TOKEN_URL = 'https://oauth2.googleapis.com/token'
const FCM_SCOPE = 'https://www.googleapis.com/auth/firebase.messaging'
let fcmCached = { token: '', exp: 0 }
let saCached = { raw: '', sa: null }

function serviceAccount(env) {
  const raw = env.FCM_SERVICE_ACCOUNT
  if (!raw) throw new Error('FCM_SERVICE_ACCOUNT is not set')
  if (saCached.raw !== raw) {
    const sa = JSON.parse(raw)
    if (!sa.client_email || !sa.private_key || !sa.project_id) throw new Error('FCM_SERVICE_ACCOUNT lacks client_email, private_key or project_id')
    saCached = { raw, sa }
  }
  return saCached.sa
}

export async function fcmAccessToken(env, now = Date.now()) {
  if (fcmCached.token && now < fcmCached.exp - 60 * 1000) return fcmCached.token
  const sa = serviceAccount(env)
  const iat = Math.floor(now / 1000)
  const header = b64url(JSON.stringify({ alg: 'RS256', typ: 'JWT', ...(sa.private_key_id ? { kid: sa.private_key_id } : {}) }))
  const claims = b64url(JSON.stringify({ iss: sa.client_email, scope: FCM_SCOPE, aud: GOOGLE_TOKEN_URL, iat, exp: iat + 3600 }))
  const key = await crypto.subtle.importKey('pkcs8', pemToDer(sa.private_key), { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' }, false, ['sign'])
  const sig = await crypto.subtle.sign('RSASSA-PKCS1-v1_5', key, new TextEncoder().encode(header + '.' + claims))
  const assertion = header + '.' + claims + '.' + b64url(new Uint8Array(sig))
  const r = await fetch(GOOGLE_TOKEN_URL, {
    method: 'POST',
    headers: { 'content-type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ grant_type: 'urn:ietf:params:oauth:grant-type:jwt-bearer', assertion }).toString(),
  })
  const text = await r.text().catch(() => '')
  let res = {}
  try {
    res = JSON.parse(text)
  } catch {}
  if (!r.ok || !res.access_token) throw new Error(`google token ${r.status}: ${text.slice(0, 200)}`)
  fcmCached = { token: res.access_token, exp: now + (Number(res.expires_in) || 3600) * 1000 }
  return fcmCached.token
}

export function resetFCMToken() {
  fcmCached = { token: '', exp: 0 }
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
