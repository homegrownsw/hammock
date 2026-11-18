// Runs the worker against a fake KV and a fake APNs (node --test).
import { test } from 'node:test'
import assert from 'node:assert/strict'
import worker, { apnsJWT, resetJWT, clientKey, forgetUnused, resetFCMToken } from '../src/index.js'

// Enough of D1 for the relay's statements (counting the queries made).
function fakeD1() {
  const rows = new Map()
  const db = {
    rows,
    queries: 0,
    prepare(sql) {
      return {
        bind(...a) {
          return {
            run: async () => {
              db.queries++
              if (sql.startsWith('INSERT')) rows.set(a[0], { token: a[1], sandbox: a[2], secret: a[3], last_used: a[4], created: a[4], provider: a[5] })
              if (sql === 'DELETE FROM devices WHERE id = ?') rows.delete(a[0])
              if (sql === 'DELETE FROM devices WHERE token = ?') for (const [id, r] of rows) if (r.token === a[0]) rows.delete(id)
              if (sql === 'DELETE FROM devices WHERE last_used < ? OR (last_used IS NULL AND created < ?)')
                for (const [id, r] of rows) if (r.last_used == null ? r.created < a[1] : r.last_used < a[0]) rows.delete(id)
              if (sql === 'UPDATE devices SET last_used = ? WHERE id = ?' && rows.has(a[1])) rows.get(a[1]).last_used = a[0]
              if (sql.startsWith('UPDATE devices SET token') && rows.has(a[4])) Object.assign(rows.get(a[4]), { token: a[0], sandbox: a[1], provider: a[2], last_used: a[3] })
            },
            first: async () => (db.queries++, rows.get(a[0]) ?? null),
          }
        },
      }
    },
    async batch(stmts) {
      for (const st of stmts) await st.run()
    },
  }
  return db
}

// Cloudflare's rate limiter, counting per key.
function fakeLimiter(limit) {
  const n = new Map()
  return { limit: async ({ key }) => ({ success: (n.set(key, (n.get(key) ?? 0) + 1), n.get(key) <= limit) }) }
}

async function testEnv() {
  const kp = await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify'])
  const der = new Uint8Array(await crypto.subtle.exportKey('pkcs8', kp.privateKey))
  const pem = '-----BEGIN PRIVATE KEY-----\n' + btoa(String.fromCharCode(...der)) + '\n-----END PRIVATE KEY-----'
  return { env: { APNS_KEY: pem, APNS_KEY_ID: 'KEYID12345', APNS_TEAM_ID: 'TEAM123456', APNS_TOPIC: 'com.homegrownsw.hammock', DB: fakeD1(), REQUESTS: fakeLimiter(200), SENDS: fakeLimiter(60), REGISTERS: fakeLimiter(10) }, publicKey: kp.publicKey }
}

const post = (path, body, ip = '192.0.2.1') =>
  new Request('https://relay.test' + path, { method: 'POST', body: JSON.stringify(body), headers: { 'cf-connecting-ip': ip } })

test('register, send through APNs, unregister', async () => {
  resetJWT()
  const { env, publicKey } = await testEnv()
  const token = 'ab'.repeat(32)
  const reg = await (await worker.fetch(post('/register', { token, sandbox: true }), env)).json()
  assert.match(reg.id, /^[0-9a-f]{32}$/)

  const sent = []
  globalThis.fetch = async (url, init) => {
    sent.push({ url, init })
    return new Response(null, { status: 200 })
  }
  const r = await worker.fetch(post('/send', { id: reg.id, payload: 'c2VjcmV0', thread: 'pn_1', collapse: 'at_pn_1', category: 'join' }), env)
  assert.equal(r.status, 200)
  assert.equal(sent.length, 1)
  assert.equal(sent[0].url, 'https://api.sandbox.push.apple.com/3/device/' + token)
  const body = JSON.parse(sent[0].init.body)
  assert.equal(body.e, 'c2VjcmV0')
  assert.equal(body.aps['mutable-content'], 1)
  assert.equal(body.aps.category, 'join')
  assert.equal(body.aps['thread-id'], 'pn_1')
  assert.equal(sent[0].init.headers['apns-collapse-id'], 'at_pn_1')

  // The JWT verifies with the key's public half.
  const [h, c, s] = sent[0].init.headers.authorization.slice(7).split('.')
  const sig = Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), (x) => x.charCodeAt(0))
  assert.ok(await crypto.subtle.verify({ name: 'ECDSA', hash: 'SHA-256' }, publicKey, sig, new TextEncoder().encode(h + '.' + c)))
  assert.equal(JSON.parse(atob(h)).kid, 'KEYID12345')

  // Unknown ids and bad payloads are refused; unregistering needs the secret.
  assert.equal((await worker.fetch(post('/send', { id: 'f'.repeat(32), payload: 'eA==' }), env)).status, 404)
  assert.equal((await worker.fetch(post('/send', { id: reg.id, payload: '<script>' }), env)).status, 400)
  assert.equal((await worker.fetch(post('/unregister', { id: reg.id, secret: 'nope' }), env)).status, 404)
  assert.equal((await worker.fetch(post('/unregister', { id: reg.id, secret: reg.secret }), env)).status, 200)
  assert.equal((await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)).status, 404)
})

test('a removed app is forgotten', async () => {
  resetJWT()
  const { env } = await testEnv()
  const reg = await (await worker.fetch(post('/register', { token: 'cd'.repeat(32) }), env)).json()
  globalThis.fetch = async () => new Response(JSON.stringify({ reason: 'Unregistered' }), { status: 410 })
  assert.equal((await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)).status, 410)
  assert.equal(env.DB.rows.has(reg.id), false)
})

test('a token for another topic is forgotten too', async () => {
  resetJWT()
  const { env } = await testEnv()
  const reg = await (await worker.fetch(post('/register', { token: 'ce'.repeat(32) }), env)).json()
  globalThis.fetch = async () => new Response(JSON.stringify({ reason: 'DeviceTokenNotForTopic' }), { status: 400 })
  const r = await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)
  assert.equal(r.status, 410)
  assert.equal(env.DB.rows.has(reg.id), false)
})

test('sends are rate limited', async () => {
  resetJWT()
  const { env } = await testEnv()
  const reg = await (await worker.fetch(post('/register', { token: 'ef'.repeat(32) }), env)).json()
  globalThis.fetch = async () => new Response(null, { status: 200 })
  let last
  for (let i = 0; i < 61; i++) last = await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)
  assert.equal(last.status, 429)
})

test('a phone keeps its id with its secret; nobody else can take it', async () => {
  resetJWT()
  const { env } = await testEnv()
  const token = '12'.repeat(32)
  const first = await (await worker.fetch(post('/register', { token }), env)).json()
  // Someone who learnt the token registers it: a new id, the phone's stays.
  const other = await (await worker.fetch(post('/register', { token }), env)).json()
  assert.notEqual(other.id, first.id)
  assert.ok(env.DB.rows.has(first.id))
  // Nor can they move the phone's id to their own token without its secret.
  const stolen = await (await worker.fetch(post('/register', { token: '99'.repeat(32), id: first.id, secret: 'guess' }), env)).json()
  assert.notEqual(stolen.id, first.id)
  assert.equal(env.DB.rows.get(first.id).token, token)
  // Proving their own row's secret, for the phone's token, doesn't drop the
  // phone's row either: rows are never deleted by token.
  await worker.fetch(post('/register', { token, id: other.id, secret: other.secret }), env)
  assert.ok(env.DB.rows.has(first.id))
  // The phone, with a new token, keeps its id.
  const token2 = '34'.repeat(32)
  // Its old row (from before the Keychain) it drops itself, with that row's secret.
  const old = await (await worker.fetch(post('/register', { token }), env)).json()
  assert.equal((await worker.fetch(post('/unregister', old), env)).status, 200)
  assert.ok(!env.DB.rows.has(old.id))
  const again = await (await worker.fetch(post('/register', { token: token2, sandbox: true, id: first.id, secret: first.secret }), env)).json()
  assert.deepEqual(again, first)
  assert.equal(env.DB.rows.get(first.id).token, token2)
  const sent = []
  globalThis.fetch = async (url) => (sent.push(url), new Response(null, { status: 200 }))
  assert.equal((await worker.fetch(post('/send', { id: first.id, payload: 'eA==' }), env)).status, 200)
  assert.equal(sent[0], 'https://api.sandbox.push.apple.com/3/device/' + token2)
})

test('a forgotten phone comes back with its id and secret, and sends reach it', async () => {
  resetJWT()
  const { env } = await testEnv()
  const token = '21'.repeat(32)
  const first = await (await worker.fetch(post('/register', { token }), env)).json()
  // Unused for 60 days: forgotten; a Mac sending to it is told it's gone.
  env.DB.rows.get(first.id).last_used = '2000-01-01 00:00:00'
  await worker.scheduled({}, env)
  assert.ok(!env.DB.rows.has(first.id))
  globalThis.fetch = async () => new Response(null, { status: 200 })
  assert.equal((await worker.fetch(post('/send', { id: first.id, payload: 'eA==' }), env)).status, 404)
  // Its weekly refresh brings the same id back, with the same secret.
  const back = await (await worker.fetch(post('/register', { token, id: first.id, secret: first.secret }), env)).json()
  assert.deepEqual(back, first)
  assert.equal((await worker.fetch(post('/send', { id: first.id, payload: 'eA==' }), env)).status, 200)
  // A refresh keeps it from being forgotten.
  env.DB.rows.get(first.id).last_used = '2000-01-01 00:00:00'
  await worker.fetch(post('/register', { token, id: first.id, secret: first.secret }), env)
  await worker.scheduled({}, env)
  assert.ok(env.DB.rows.has(first.id))
  // Once it's back, nobody else can claim the id; and a secret that isn't
  // one the relay could have handed out doesn't bring an id back.
  const other = await (await worker.fetch(post('/register', { token: '43'.repeat(32), id: first.id, secret: 'f'.repeat(48) }), env)).json()
  assert.notEqual(other.id, first.id)
  const made = await (await worker.fetch(post('/register', { token, id: 'e'.repeat(32), secret: 'short' }), env)).json()
  assert.notEqual(made.id, 'e'.repeat(32))
})

test('registrations are rate limited per address', async () => {
  const { env } = await testEnv()
  let last
  for (let i = 0; i < 11; i++) last = await worker.fetch(post('/register', { token: i.toString(16).padStart(64, 'a') }), env)
  assert.equal(last.status, 429)
  assert.equal(env.DB.rows.size, 10)
  // Another address still gets in.
  assert.equal((await worker.fetch(post('/register', { token: 'b'.repeat(64) }, '198.51.100.7'), env)).status, 200)
})

test('junk is turned away before the database', async () => {
  const { env } = await testEnv()
  assert.equal((await worker.fetch(post('/wp-login.php', {}), env)).status, 404)
  assert.equal((await worker.fetch(new Request('https://relay.test/send'), env)).status, 405)
  const big = { id: 'a'.repeat(32), payload: 'A'.repeat(5000) }
  assert.equal((await worker.fetch(post('/send', big), env)).status, 413)
  assert.equal((await worker.fetch(post('/send', { id: 'nope', payload: 'eA==' }), env)).status, 404)
  assert.equal((await worker.fetch(post('/send', { id: 'a'.repeat(32), payload: '<x>' }), env)).status, 400)
  assert.equal((await worker.fetch(post('/unregister', { id: 'nope', secret: 'x' }), env)).status, 404)
  assert.equal(env.DB.queries, 0)
  // A made-up id is limited before it's looked up.
  for (let i = 0; i < 70; i++) await worker.fetch(post('/send', { id: 'a'.repeat(32), payload: 'eA==' }), env)
  assert.equal(env.DB.queries, 60)
})

test('requests are limited per address, IPv6 by its /64', async () => {
  const { env } = await testEnv()
  let last
  for (let i = 0; i < 201; i++) last = await worker.fetch(post('/send', { id: 'b'.repeat(32), payload: 'eA==' }, `2001:db8:1:2::${i.toString(16)}`), env)
  assert.equal(last.status, 429)
  assert.equal((await worker.fetch(post('/send', { id: 'c'.repeat(32), payload: 'eA==' }, '2001:db8:1:3::1'), env)).status, 404)

  assert.equal(clientKey('192.0.2.1'), 'ip:192.0.2.1')
  assert.equal(clientKey('2001:db8:1:2::1'), 'v6:2001:db8:1:2::/64')
  assert.equal(clientKey('2001:0DB8:0001:0002:aaaa:bbbb:cccc:dddd'), 'v6:2001:db8:1:2::/64')
  assert.equal(clientKey('2001:db8::1'), 'v6:2001:db8:0:0::/64')
  assert.equal(clientKey('::ffff:192.0.2.9'), 'ip:192.0.2.9')
})

test('sends keep a device; unused ones are forgotten', async () => {
  resetJWT()
  const { env } = await testEnv()
  const kept = await (await worker.fetch(post('/register', { token: '56'.repeat(32) }), env)).json()
  const unused = await (await worker.fetch(post('/register', { token: '78'.repeat(32) }), env)).json()
  const old = '2000-01-01 00:00:00'
  env.DB.rows.get(kept.id).last_used = old
  env.DB.rows.get(unused.id).last_used = old
  globalThis.fetch = async () => new Response(null, { status: 200 })
  assert.equal((await worker.fetch(post('/send', { id: kept.id, payload: 'eA==' }), env)).status, 200)
  assert.notEqual(env.DB.rows.get(kept.id).last_used, old)
  // Used today: not written again.
  const q = env.DB.queries
  await worker.fetch(post('/send', { id: kept.id, payload: 'eA==' }), env)
  assert.equal(env.DB.queries, q + 1)

  // Rows the worker wrote before it set last_used: counted from creation.
  const oldWorker = await (await worker.fetch(post('/register', { token: '79'.repeat(32) }), env)).json()
  const newWorker = await (await worker.fetch(post('/register', { token: '7a'.repeat(32) }), env)).json()
  Object.assign(env.DB.rows.get(oldWorker.id), { last_used: null, created: old })
  env.DB.rows.get(newWorker.id).last_used = null
  await worker.scheduled({}, env)
  assert.ok(env.DB.rows.has(kept.id))
  assert.ok(!env.DB.rows.has(unused.id))
  assert.ok(!env.DB.rows.has(oldWorker.id))
  assert.ok(env.DB.rows.has(newWorker.id))
  env.DB.rows.delete(newWorker.id)
  // 60 days on, the kept one goes too.
  await forgetUnused(env, Date.now() + 61 * 24 * 60 * 60 * 1000)
  assert.equal(env.DB.rows.size, 0)
})

test('errors say nothing of APNs or the worker', async () => {
  resetJWT()
  const { env } = await testEnv()
  const reg = await (await worker.fetch(post('/register', { token: '9a'.repeat(32) }), env)).json()
  const logged = []
  const oldError = console.error
  console.error = (...a) => logged.push(a.join(' '))
  try {
    globalThis.fetch = async () => new Response(JSON.stringify({ reason: 'TooManyProviderTokenUpdates' }), { status: 429 })
    let r = await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)
    assert.equal(r.status, 502)
    assert.deepEqual(await r.json(), { error: 'refused' })
    // A 400 that isn't about the token: refused, and the phone is kept.
    globalThis.fetch = async () => new Response(JSON.stringify({ reason: 'BadCollapseId' }), { status: 400 })
    assert.equal((await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)).status, 502)
    assert.ok(env.DB.rows.has(reg.id))
    globalThis.fetch = async () => new Response(JSON.stringify({ reason: 'BadDeviceToken' }), { status: 400 })
    r = await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)
    assert.equal(r.status, 410)
    assert.deepEqual(await r.json(), { error: 'gone' })
    assert.ok(!env.DB.rows.has(reg.id))
    // An exception inside.
    env.DB.prepare = () => {
      throw new Error('D1_ERROR: secret table detail')
    }
    r = await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)
    assert.equal(r.status, 500)
    assert.deepEqual(await r.json(), { error: 'internal error' })
    assert.ok(logged.some((l) => l.includes('secret table detail')))
    assert.ok(logged.some((l) => l.includes('TooManyProviderTokenUpdates')))
  } finally {
    console.error = oldError
  }
})

test('only known categories reach Apple', async () => {
  resetJWT()
  const { env } = await testEnv()
  const reg = await (await worker.fetch(post('/register', { token: 'bc'.repeat(32) }), env)).json()
  const sent = []
  globalThis.fetch = async (url, init) => (sent.push(JSON.parse(init.body)), new Response(null, { status: 200 }))
  await worker.fetch(post('/send', { id: reg.id, payload: 'eA==', category: 'evil' }), env)
  await worker.fetch(post('/send', { id: reg.id, payload: 'eA==', category: 'hammockkey' }), env)
  assert.equal(sent[0].aps.category, undefined)
  assert.equal(sent[1].aps.category, 'hammockkey')
  assert.equal(sent[1].aps['interruption-level'], 'time-sensitive')
})

test('a clear is a silent push, with nothing but the ciphertext', async () => {
  resetJWT()
  const { env } = await testEnv()
  const token = 'cf'.repeat(32)
  const reg = await (await worker.fetch(post('/register', { token }), env)).json()
  const sent = []
  globalThis.fetch = async (url, init) => {
    sent.push({ url, init })
    return new Response(null, { status: 200 })
  }
  // Hints a send would pass on are ignored.
  const r = await worker.fetch(post('/clear', { id: reg.id, payload: 'c2VjcmV0', thread: 't', collapse: 'c', category: 'grant' }), env)
  assert.equal(r.status, 200)
  assert.equal(sent.length, 1)
  assert.equal(sent[0].url, 'https://api.push.apple.com/3/device/' + token)
  assert.deepEqual(JSON.parse(sent[0].init.body), { aps: { 'content-available': 1 }, e: 'c2VjcmV0' })
  assert.equal(sent[0].init.headers['apns-push-type'], 'background')
  assert.equal(sent[0].init.headers['apns-priority'], '5')
  assert.equal(sent[0].init.headers['apns-collapse-id'], undefined)
  // Checked as a send is.
  assert.equal((await worker.fetch(post('/clear', { id: 'f'.repeat(32), payload: 'eA==' }), env)).status, 404)
  assert.equal((await worker.fetch(post('/clear', { id: reg.id, payload: '<script>' }), env)).status, 400)
})

// ---------- Android phones, through FCM ----------

const FCM_TOKEN = 'dGVzdC1mY20:APA91b' + 'Hx_y-Z0'.repeat(20)

// A test env with a Firebase service account (a fresh RSA key) besides APNs'.
async function fcmEnv() {
  const { env } = await testEnv()
  const kp = await crypto.subtle.generateKey({ name: 'RSASSA-PKCS1-v1_5', modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: 'SHA-256' }, true, ['sign', 'verify'])
  const der = new Uint8Array(await crypto.subtle.exportKey('pkcs8', kp.privateKey))
  const pem = '-----BEGIN PRIVATE KEY-----\n' + btoa(String.fromCharCode(...der)) + '\n-----END PRIVATE KEY-----\n'
  env.FCM_SERVICE_ACCOUNT = JSON.stringify({ type: 'service_account', project_id: 'hammock-test', private_key_id: 'kid1', private_key: pem, client_email: 'relay@hammock-test.iam.gserviceaccount.com' })
  return { env, publicKey: kp.publicKey }
}

// Google's token endpoint and FCM, recording what's sent to each.
function fakeGoogle(fcmResponse = () => new Response(JSON.stringify({ name: 'projects/hammock-test/messages/1' }), { status: 200 })) {
  const calls = { token: [], fcm: [] }
  globalThis.fetch = async (url, init) => {
    if (url === 'https://oauth2.googleapis.com/token') {
      calls.token.push({ url, init })
      return new Response(JSON.stringify({ access_token: 'ya29.test', expires_in: 3599, token_type: 'Bearer' }), { status: 200 })
    }
    calls.fcm.push({ url, init, body: JSON.parse(init.body) })
    return fcmResponse()
  }
  return calls
}

test('an Android phone registers with an FCM token', async () => {
  const { env } = await fcmEnv()
  const r = await worker.fetch(post('/register', { token: FCM_TOKEN, provider: 'fcm', sandbox: true }), env)
  assert.equal(r.status, 200)
  const reg = await r.json()
  assert.match(reg.id, /^[0-9a-f]{32}$/)
  assert.equal(env.DB.rows.get(reg.id).provider, 'fcm')
  assert.equal(env.DB.rows.get(reg.id).sandbox, 0) // APNs' only
  // Its id and secret keep it, as an iPhone's do.
  const again = await (await worker.fetch(post('/register', { token: FCM_TOKEN + 'x', provider: 'fcm', id: reg.id, secret: reg.secret }), env)).json()
  assert.deepEqual(again, reg)
  assert.equal(env.DB.rows.get(reg.id).token, FCM_TOKEN + 'x')

  // Tokens are checked for their provider; unknown providers are refused.
  const bad = (body) => worker.fetch(post('/register', body), env).then((r) => r.status)
  assert.equal(await bad({ token: 'short:1', provider: 'fcm' }), 400)
  assert.equal(await bad({ token: FCM_TOKEN + '/', provider: 'fcm' }), 400)
  assert.equal(await bad({ token: 'a'.repeat(1025), provider: 'fcm' }), 400)
  assert.equal(await bad({ token: FCM_TOKEN, provider: 'apns' }), 400)
  assert.equal(await bad({ token: FCM_TOKEN }), 400)
  assert.equal(await bad({ token: 'ab'.repeat(32), provider: 'gcm' }), 400)
  // A relay without Firebase's key keeps no Android phones.
  delete env.FCM_SERVICE_ACCOUNT
  assert.equal(await bad({ token: FCM_TOKEN, provider: 'fcm' }), 501)
})

test('iPhones register as before', async () => {
  const { env } = await fcmEnv()
  const token = 'a1'.repeat(32)
  const reg = await (await worker.fetch(post('/register', { token, sandbox: true }), env)).json()
  assert.equal(env.DB.rows.get(reg.id).provider, 'apns')
  assert.equal(env.DB.rows.get(reg.id).sandbox, 1)
  const explicit = await (await worker.fetch(post('/register', { token, provider: 'apns' }), env)).json()
  assert.equal(env.DB.rows.get(explicit.id).provider, 'apns')
  // And their sends go to Apple only.
  resetJWT()
  const calls = fakeGoogle()
  const apple = []
  const google = globalThis.fetch
  globalThis.fetch = async (url, init) => (url.startsWith('https://api.') ? (apple.push(url), new Response(null, { status: 200 })) : google(url, init))
  assert.equal((await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)).status, 200)
  assert.deepEqual(apple, ['https://api.sandbox.push.apple.com/3/device/' + token])
  assert.equal(calls.token.length + calls.fcm.length, 0)
})

test('a send to an Android phone is a data-only FCM message', async () => {
  resetFCMToken()
  const { env, publicKey } = await fcmEnv()
  const reg = await (await worker.fetch(post('/register', { token: FCM_TOKEN, provider: 'fcm' }), env)).json()
  const calls = fakeGoogle()
  const r = await worker.fetch(post('/send', { id: reg.id, payload: 'c2VjcmV0', thread: 'pn_1', collapse: 'at_pn_1', category: 'hammockkey' }), env)
  assert.equal(r.status, 200)
  assert.equal(calls.fcm.length, 1)
  assert.equal(calls.fcm[0].url, 'https://fcm.googleapis.com/v1/projects/hammock-test/messages:send')
  assert.equal(calls.fcm[0].init.headers.authorization, 'Bearer ya29.test')
  assert.deepEqual(calls.fcm[0].body, {
    message: {
      token: FCM_TOKEN,
      data: { e: 'c2VjcmV0', category: 'hammockkey', thread: 'pn_1', collapse: 'at_pn_1' },
      android: { priority: 'high', ttl: '3600s', collapse_key: 'at_pn_1' },
    },
  })

  // The access token came from a JWT for the service account, signed with its key.
  assert.equal(calls.token.length, 1)
  const form = new URLSearchParams(calls.token[0].init.body)
  assert.equal(form.get('grant_type'), 'urn:ietf:params:oauth:grant-type:jwt-bearer')
  const [h, c, s] = form.get('assertion').split('.')
  const unb64 = (x) => Uint8Array.from(atob(x.replace(/-/g, '+').replace(/_/g, '/')), (ch) => ch.charCodeAt(0))
  assert.ok(await crypto.subtle.verify('RSASSA-PKCS1-v1_5', publicKey, unb64(s), new TextEncoder().encode(h + '.' + c)))
  assert.deepEqual(JSON.parse(new TextDecoder().decode(unb64(h))), { alg: 'RS256', typ: 'JWT', kid: 'kid1' })
  const claims = JSON.parse(new TextDecoder().decode(unb64(c)))
  assert.equal(claims.iss, 'relay@hammock-test.iam.gserviceaccount.com')
  assert.equal(claims.scope, 'https://www.googleapis.com/auth/firebase.messaging')
  assert.equal(claims.aud, 'https://oauth2.googleapis.com/token')
  assert.equal(claims.exp - claims.iat, 3600)

  // The next send reuses the access token; empty hints and unknown categories are left out.
  await worker.fetch(post('/send', { id: reg.id, payload: 'eA==', category: 'evil' }), env)
  assert.equal(calls.token.length, 1)
  assert.deepEqual(calls.fcm[1].body.message.data, { e: 'eA==' })
  assert.deepEqual(calls.fcm[1].body.message.android, { priority: 'high', ttl: '3600s' })
  assert.equal(calls.fcm[1].body.message.notification, undefined)
})

test('a clear to an Android phone is a normal-priority data message', async () => {
  resetFCMToken()
  const { env } = await fcmEnv()
  const reg = await (await worker.fetch(post('/register', { token: FCM_TOKEN, provider: 'fcm' }), env)).json()
  const calls = fakeGoogle()
  const r = await worker.fetch(post('/clear', { id: reg.id, payload: 'c2VjcmV0', thread: 't', collapse: 'c', category: 'grant' }), env)
  assert.equal(r.status, 200)
  assert.deepEqual(calls.fcm[0].body, { message: { token: FCM_TOKEN, data: { clear: '1', e: 'c2VjcmV0' }, android: { priority: 'normal', ttl: '3600s' } } })
})

test('an Android phone FCM no longer knows is forgotten', async () => {
  const fcmError = (status, code, message, errorCode) => () =>
    new Response(JSON.stringify({ error: { code: status, message, status: code, details: errorCode ? [{ '@type': 'type.googleapis.com/google.firebase.fcm.v1.FcmError', errorCode }] : [] } }), { status })
  const cases = [
    [fcmError(404, 'NOT_FOUND', 'Requested entity was not found.', 'UNREGISTERED'), 410],
    [fcmError(400, 'INVALID_ARGUMENT', 'The registration token is not a valid FCM registration token', 'INVALID_ARGUMENT'), 410],
    [fcmError(403, 'PERMISSION_DENIED', 'SenderId mismatch', 'SENDER_ID_MISMATCH'), 410],
    // Not about the token: refused, and the phone is kept.
    [fcmError(400, 'INVALID_ARGUMENT', "Invalid value at 'message.android.ttl'", 'INVALID_ARGUMENT'), 502],
    [fcmError(429, 'RESOURCE_EXHAUSTED', 'Quota exceeded', 'QUOTA_EXCEEDED'), 502],
    [fcmError(503, 'UNAVAILABLE', 'The service is currently unavailable.', 'UNAVAILABLE'), 502],
  ]
  const oldError = console.error
  console.error = () => {}
  try {
    for (const [respond, want] of cases) {
      resetFCMToken()
      const { env } = await fcmEnv()
      const reg = await (await worker.fetch(post('/register', { token: FCM_TOKEN, provider: 'fcm' }), env)).json()
      fakeGoogle(respond)
      const r = await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)
      assert.equal(r.status, want)
      assert.deepEqual(await r.json(), { error: want === 410 ? 'gone' : 'refused' })
      assert.equal(env.DB.rows.has(reg.id), want !== 410)
    }
    // A turned-down access token is fetched afresh next time.
    resetFCMToken()
    const { env } = await fcmEnv()
    const reg = await (await worker.fetch(post('/register', { token: FCM_TOKEN, provider: 'fcm' }), env)).json()
    const calls = fakeGoogle(fcmError(401, 'UNAUTHENTICATED', 'Request had invalid authentication credentials.'))
    assert.equal((await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)).status, 502)
    await worker.fetch(post('/send', { id: reg.id, payload: 'eA==' }), env)
    assert.equal(calls.token.length, 2)
    assert.ok(env.DB.rows.has(reg.id))
  } finally {
    console.error = oldError
  }
})
