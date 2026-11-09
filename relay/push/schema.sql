-- npx wrangler d1 execute hmk-push --remote --file schema.sql
CREATE TABLE IF NOT EXISTS devices (
  id      TEXT PRIMARY KEY,   -- what Macs send to
  token   TEXT NOT NULL,      -- the APNs device token
  sandbox INTEGER NOT NULL,   -- a development build (APNs' sandbox)
  secret  TEXT NOT NULL,      -- SHA-256 of the phone's secret, for unregistering
  created TEXT NOT NULL DEFAULT (datetime('now'))
);
-- Registering looks up (and replaces) a phone's earlier row by token.
CREATE INDEX IF NOT EXISTS devices_token ON devices (token);
