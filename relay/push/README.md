# Hammock push relay

A Cloudflare Worker that holds Hammock's APNs key and forwards notifications
to phones. Each Mac encrypts notifications with a key only the phone has
(AES-256-GCM), and the phone's notification extension decrypts them, so
the relay never sees what a notification says. See ADR-0007.

What it does see: each phone's APNs device token, and whether it's a
development build; and for each notification, the phone's relay id, the
ciphertext, when it was sent and from which address, and three hints it
passes to Apple: a thread and a collapse tag (keyed hashes of the pane's
or request's id, unique to the phone: they say two notifications are about
the same thing, not what it is) and the category (`join`, `grant` or
`hammockkey`, which says whether it's someone asking to join, an agent
asking permission, or a Hammock Key request). The phone takes everything
it shows, the category included, from the decrypted note, and shows
nothing of one it can't decrypt or that was sent over an hour ago.

## Set up

1. **APNs key.** developer.apple.com ▸ Certificates, IDs & Profiles ▸ Keys ▸
   + ▸ "Apple Push Notifications service (APNs)". Download the `.p8` (once
   only) and note its Key ID. The team id is 2G974K78BH.
2. **Cloudflare.** `npx wrangler login`, then from this folder:

   ```sh
   npx wrangler d1 create hmk-push           # paste its id into wrangler.toml
   npx wrangler d1 migrations apply hmk-push --remote
   npx wrangler secret put APNS_KEY < AuthKey_XXXXXXXXXX.p8
   echo XXXXXXXXXX | npx wrangler secret put APNS_KEY_ID
   echo 2G974K78BH | npx wrangler secret put APNS_TEAM_ID
   npx wrangler deploy                         # https://hmk-push.<you>.workers.dev
   ```

   The database's tables are in `migrations/`. (A database set up before
   migrations, from the old `schema.sql`, takes them too: the first only
   creates what's missing.)

   Ours also answers at https://push.homegrownsw.com (`routes` in wrangler.toml; the deploy makes its DNS
   record, as homegrownsw.com's DNS is on Cloudflare), the URL the iPhone app uses. It still answers at
   hmk-push.homegrownsw.workers.dev for phones registered before that; both reach the same Worker and
   database, so a phone's id works at either. Without a domain on Cloudflare, take `routes` out.
3. **Production.** Add the WAF rate-limiting rule (below, Costs and abuse) on push.homegrownsw.com. It only
   covers that domain: the workers.dev name bypasses it, so once phones have moved to the domain, set
   `workers_dev = false` in wrangler.toml.
4. **The app.** Build the iOS app with the relay's URL:
   `HMK_PUSH_RELAY=https://hmk-push.<you>.workers.dev` as a build setting
   (`just ios-device` passes it through).

## Deploying a change

In this order, each time:

- [ ] `npm test`
- [ ] `npx wrangler d1 migrations apply hmk-push --remote`: migrations
      first, so the new worker never runs against an old table (the old
      worker is fine with the new one: it only adds columns).
- [ ] `npx wrangler deploy`
- [ ] the WAF rate-limiting rule is still in place on the relay's route
      (Security ▸ WAF ▸ Rate limiting rules), blocking every other path.

Rows the old worker wrote between the two steps have no `last_used`;
they're forgotten 60 days from when they were created.

## Routes

- `POST /register {token, sandbox, id?, secret?}` → `{id, secret}`: a phone
  registering, 10 a minute per client address. With the `id` and `secret`
  it had (a new token, the app reinstalled, or its weekly refresh, which
  keeps it from being forgotten), it keeps that id, and one forgotten comes
  back with the same id and secret while no other row holds it; without,
  it gets a new one, and an earlier id for the same token stays (whoever
  learnt a phone's token can't cut it off) until it goes unused.
- `POST /unregister {id, secret}`: a phone dropping an id it no longer
  uses (the one it kept before its credentials moved to the Keychain).

Rows are never deleted by token, not even when a request proves another
row's secret: that proves the row, not the token, so anyone who learnt a
phone's token could register a row of their own for it, prove its secret,
and cut the phone off. A phone drops its own old row with `/unregister`,
and the rest age out unused.
- `POST /send {id, payload, thread?, collapse?, category?}`: a Mac sending
  (ciphertext in `payload`), about 60 a minute per phone at most. Any
  category but `join`, `grant` and `hammockkey` is dropped.

Any other path or method is turned away, as is a body over 4 KB. Every
route counts towards 200 requests a minute per client address; an IPv6
address counts by its /64, since one customer gets a whole /64.

A daily cron trigger forgets devices nothing has sent to (or registered)
for 60 days, so made-up registrations don't stay.

## Costs, and abuse

On Cloudflare's free plan a send costs one request and one D1 row read
(100,000 requests and 5 million reads a day), and a write at most once a
day per device (to keep its last use), so the relay handles about 100,000
notifications a day. The worker turns away whatever it can before touching
the database (junk paths, big bodies, malformed ids, too many requests from
one address or to one id), but **every request to a Worker counts**, even
one it refuses at once, and its own limits are per address: many addresses
together can still use up the free plan's daily requests and stop
notifications for everyone until the next day.

For production, also:

- add a **Cloudflare WAF rate-limiting rule** on the relay's route (it
  turns requests away before they reach the Worker, so they don't count),
  for example 300 requests a minute per IP to `/send`, `/register` and
  `/unregister`, and block every other path;
- or move to the **Workers Paid plan**, which has no daily request cap.

A stronger fix, not done yet: have the app prove itself to the relay with
App Attest or DeviceCheck when it registers, so only genuine copies of
Hammock can make ids.

Tests: `npm test` (a fake D1, rate limiter and APNs).
