// Runs the worker against a fake KV and a fake APNs (node --test).
import { test } from 'node:test'
import assert from 'node:assert/strict'
import worker, { apnsJWT, resetJWT } from '../src/index.js'

// Enough of D1 for the relay's statements.
function fakeD1() {
  const rows = new Map()
  return {
    rows,
    prepare(sql) {
      return {
        bind(...a) {
          return {
            run: async () => {
              if (sql.startsWith('INSERT')) rows.set(a[0], { token: a[1], sandbox: a[2], secret: a[3] })
              if (sql === 'DELETE FROM devices WHERE id = ?') rows.delete(a[0])
              if (sql === 'DELETE FROM devices WHERE token = ?') for (const [id, r] of rows) if (r.token === a[0]) rows.delete(id)
            },
            first: async () => rows.get(a[0]) ?? null,
          }
        },
      }
    },
    async batch(stmts) {
      for (const st of stmts) await st.run()
    },
  }
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
  return { env: { APNS_KEY: pem, APNS_KEY_ID: 'KEYID12345', APNS_TEAM_ID: 'TEAM123456', APNS_TOPIC: 'com.homegrownsw.hammock', DB: fakeD1(), SENDS: fakeLimiter(60), REGISTERS: fakeLimiter(10) }, publicKey: kp.publicKey }
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

test('registering a token again replaces its earlier id', async () => {
  const { env } = await testEnv()
  const token = '12'.repeat(32)
  const first = await (await worker.fetch(post('/register', { token }), env)).json()
  const other = await (await worker.fetch(post('/register', { token: '34'.repeat(32) }), env)).json()
  const again = await (await worker.fetch(post('/register', { token }), env)).json()
  assert.notEqual(again.id, first.id)
  assert.deepEqual([...env.DB.rows.keys()].sort(), [other.id, again.id].sort())
  globalThis.fetch = async () => new Response(null, { status: 200 })
  assert.equal((await worker.fetch(post('/send', { id: first.id, payload: 'eA==' }), env)).status, 404)
  assert.equal((await worker.fetch(post('/send', { id: again.id, payload: 'eA==' }), env)).status, 200)
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
