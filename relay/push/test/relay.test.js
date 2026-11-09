// Runs the worker against a fake KV and a fake APNs (node --test).
import { test } from 'node:test'
import assert from 'node:assert/strict'
import worker, { apnsJWT, resetJWT, clientKey, forgetUnused } from '../src/index.js'

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
              if (sql.startsWith('INSERT')) rows.set(a[0], { token: a[1], sandbox: a[2], secret: a[3], last_used: a[4] })
              if (sql === 'DELETE FROM devices WHERE id = ?') rows.delete(a[0])
              if (sql === 'DELETE FROM devices WHERE token = ?') for (const [id, r] of rows) if (r.token === a[0]) rows.delete(id)
              if (sql === 'DELETE FROM devices WHERE last_used < ?') for (const [id, r] of rows) if (r.last_used < a[0]) rows.delete(id)
              if (sql === 'UPDATE devices SET last_used = ? WHERE id = ?' && rows.has(a[1])) rows.get(a[1]).last_used = a[0]
              if (sql.startsWith('UPDATE devices SET token') && rows.has(a[3])) Object.assign(rows.get(a[3]), { token: a[0], sandbox: a[1], last_used: a[2] })
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
  // The phone, with a new token, keeps its id.
  const token2 = '34'.repeat(32)
  const again = await (await worker.fetch(post('/register', { token: token2, sandbox: true, id: first.id, secret: first.secret }), env)).json()
  assert.deepEqual(again, first)
  assert.equal(env.DB.rows.get(first.id).token, token2)
  const sent = []
  globalThis.fetch = async (url) => (sent.push(url), new Response(null, { status: 200 }))
  assert.equal((await worker.fetch(post('/send', { id: first.id, payload: 'eA==' }), env)).status, 200)
  assert.equal(sent[0], 'https://api.sandbox.push.apple.com/3/device/' + token2)
  // An id that's gone: a new one.
  env.DB.rows.delete(first.id)
  const fresh = await (await worker.fetch(post('/register', { token: token2, id: first.id, secret: first.secret }), env)).json()
  assert.notEqual(fresh.id, first.id)
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

  await worker.scheduled({}, env)
  assert.ok(env.DB.rows.has(kept.id))
  assert.ok(!env.DB.rows.has(unused.id))
  // 60 days on, the kept one goes too.
  await forgetUnused(env, Date.now() + 61 * 24 * 60 * 60 * 1000)
  assert.equal(env.DB.rows.size, 0)
})
