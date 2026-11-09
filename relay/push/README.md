# Hammock push relay

A Cloudflare Worker that holds Hammock's APNs key and forwards notifications
to phones. It only ever sees APNs device tokens and ciphertext: each Mac
encrypts notifications with a key only the phone has (AES-256-GCM), and
the phone's notification extension decrypts them. See ADR-0007.

## Set up

1. **APNs key.** developer.apple.com ▸ Certificates, IDs & Profiles ▸ Keys ▸
   + ▸ "Apple Push Notifications service (APNs)". Download the `.p8` (once
   only) and note its Key ID. The team id is 2G974K78BH.
2. **Cloudflare.** `npx wrangler login`, then from this folder:

   ```sh
   npx wrangler d1 create hmk-push           # paste its id into wrangler.toml
   npx wrangler d1 execute hmk-push --remote --file schema.sql
   npx wrangler secret put APNS_KEY < AuthKey_XXXXXXXXXX.p8
   echo XXXXXXXXXX | npx wrangler secret put APNS_KEY_ID
   echo 2G974K78BH | npx wrangler secret put APNS_TEAM_ID
   npx wrangler deploy                         # prints https://hmk-push.<you>.workers.dev
   ```
3. **The app.** Build the iOS app with the relay's URL:
   `HMK_PUSH_RELAY=https://hmk-push.<you>.workers.dev` as a build setting
   (`just ios-device` passes it through).

## Routes

- `POST /register {token, sandbox}` → `{id, secret}`: a phone registering
  (again: its earlier id for that token is dropped), 10 a minute per IP.
- `POST /unregister {id, secret}`
- `POST /send {id, payload, thread?, collapse?, category?}`: a Mac sending
  (ciphertext in `payload`), about 60 a minute per phone at most.

On Cloudflare's free plan a send costs one request and one D1 row read
(100,000 requests and 5 million reads a day), so the relay handles about
100,000 notifications a day; registrations are the only writes.

Tests: `npm test` (a fake D1, rate limiter and APNs).
