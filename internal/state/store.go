// Package state persists the daemon's model (Groups, Tabs, Panes) and per-pane
// runtime records in SQLite.
package state

import (
	"database/sql"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Store is the SQLite-backed persistence layer.
type Store struct {
	db     *sql.DB
	audits atomic.Int64 // events appended since opening (pruned every auditPruneEvery)
}

// The audit log keeps at most auditKeepRows events and none older than
// auditKeepAge: enough history to look back on, without growing for ever.
const (
	auditKeepRows   = 50000
	auditKeepFailed = 5000 // failed sign-ins and joins, on top (PruneAudit)
	auditKeepAge    = 90 * 24 * time.Hour
	auditPruneEvery = 500
)

// PaneRuntime records how to reach a pane's holder process.
type PaneRuntime struct {
	PaneID string
	Socket string
	Token  string // the holder's client secret ("" for holders started before there was one)
}

const schema = `
CREATE TABLE IF NOT EXISTS entities (
  kind TEXT NOT NULL,
  id   TEXT NOT NULL,
  data BLOB NOT NULL,
  PRIMARY KEY (kind, id)
);
CREATE TABLE IF NOT EXISTS pane_runtime (
  pane_id TEXT PRIMARY KEY,
  socket  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
  id        TEXT PRIMARY KEY,
  name      TEXT NOT NULL,
  platform  TEXT NOT NULL,
  pubkey    BLOB NOT NULL,
  paired    INTEGER NOT NULL,
  last_seen INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS threads (
  id        TEXT PRIMARY KEY,
  harness   TEXT NOT NULL,
  native_id TEXT NOT NULL,
  data      BLOB NOT NULL,
  UNIQUE (harness, native_id)
);
CREATE TABLE IF NOT EXISTS thread_records (
  thread_id TEXT NOT NULL,
  seq       INTEGER NOT NULL,
  hash      TEXT NOT NULL,
  data      BLOB NOT NULL,
  PRIMARY KEY (thread_id, seq)
);
CREATE TABLE IF NOT EXISTS audit (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  ts     INTEGER NOT NULL,
  actor  TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL,
  detail TEXT NOT NULL
);
`

// Open opens (creating if needed) the store at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("state: schema: %w", err)
	}
	// Holders gained a client secret (2026-10-31).
	if err := addColumn(db, "pane_runtime", "token", "TEXT NOT NULL DEFAULT ''"); err != nil {
		db.Close()
		return nil, fmt.Errorf("state: migrate pane_runtime: %w", err)
	}
	// Audit events gained a readable form (2026-11-02): who did it and what,
	// in words, as things were named then.
	for _, col := range []string{"who", "summary", "kind"} {
		if err := addColumn(db, "audit", col, "TEXT NOT NULL DEFAULT ''"); err != nil {
			db.Close()
			return nil, fmt.Errorf("state: migrate audit: %w", err)
		}
	}
	// The audit log is bounded (it wasn't before 2026-11-09). Not pruning
	// it (the disk is full, say) mustn't keep the daemon from starting.
	if err := (&Store{db: db}).PruneAudit(auditKeepRows, auditKeepFailed, auditKeepAge); err != nil {
		slog.Warn("state: prune audit", "err", err)
	}
	// Spaces were renamed Groups (2026-10-30); their records keep their ids.
	if _, err := db.Exec(`UPDATE entities SET kind='group' WHERE kind='space'`); err != nil {
		db.Close()
		return nil, fmt.Errorf("state: migrate spaces: %w", err)
	}
	return &Store{db: db}, nil
}

// addColumn adds a column to table unless it already has it.
func addColumn(db *sql.DB, table, col, decl string) error {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == col {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col + ` ` + decl)
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) put(kind, id string, m proto.Message) error {
	b, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO entities(kind,id,data) VALUES(?,?,?)
		ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data`, kind, id, b)
	return err
}

func (s *Store) del(kind, id string) error {
	_, err := s.db.Exec(`DELETE FROM entities WHERE kind=? AND id=?`, kind, id)
	return err
}

// PutGroup upserts a group.
func (s *Store) PutGroup(v *pb.Group) error { return s.put("group", v.Id, v) }

// PutTab upserts a tab.
func (s *Store) PutTab(v *pb.Tab) error { return s.put("tab", v.Id, v) }

// PutPane upserts a pane.
func (s *Store) PutPane(v *pb.Pane) error { return s.put("pane", v.Id, v) }

// DeleteGroup removes a group.
func (s *Store) DeleteGroup(id string) error { return s.del("group", id) }

// DeleteTab removes a tab.
func (s *Store) DeleteTab(id string) error { return s.del("tab", id) }

// DeleteRuntime removes a pane's runtime record (its holder is gone).
func (s *Store) DeleteRuntime(paneID string) error {
	_, err := s.db.Exec(`DELETE FROM pane_runtime WHERE pane_id=?`, paneID)
	return err
}

// DeletePane removes a pane and its runtime record.
func (s *Store) DeletePane(id string) error {
	if err := s.del("pane", id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM pane_runtime WHERE pane_id=?`, id)
	return err
}

// PutRuntime records a pane's holder socket.
func (s *Store) PutRuntime(r PaneRuntime) error {
	_, err := s.db.Exec(`INSERT INTO pane_runtime(pane_id,socket,token) VALUES(?,?,?)
		ON CONFLICT(pane_id) DO UPDATE SET socket=excluded.socket, token=excluded.token`, r.PaneID, r.Socket, r.Token)
	return err
}

// Snapshot is everything loaded from the store at startup.
type Snapshot struct {
	Groups   []*pb.Group
	Tabs     []*pb.Tab
	Panes    []*pb.Pane
	Runtimes map[string]PaneRuntime
}

// Load reads the whole model.
func (s *Store) Load() (*Snapshot, error) {
	snap := &Snapshot{Runtimes: map[string]PaneRuntime{}}
	rows, err := s.db.Query(`SELECT kind, data FROM entities`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var data []byte
		if err := rows.Scan(&kind, &data); err != nil {
			return nil, err
		}
		switch kind {
		case "group":
			v := &pb.Group{}
			if err := proto.Unmarshal(data, v); err != nil {
				return nil, err
			}
			snap.Groups = append(snap.Groups, v)
		case "tab":
			v := &pb.Tab{}
			if err := proto.Unmarshal(data, v); err != nil {
				return nil, err
			}
			snap.Tabs = append(snap.Tabs, v)
		case "pane":
			v := &pb.Pane{}
			if err := proto.Unmarshal(data, v); err != nil {
				return nil, err
			}
			snap.Panes = append(snap.Panes, v)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rt, err := s.db.Query(`SELECT pane_id, socket, token FROM pane_runtime`)
	if err != nil {
		return nil, err
	}
	defer rt.Close()
	for rt.Next() {
		var r PaneRuntime
		if err := rt.Scan(&r.PaneID, &r.Socket, &r.Token); err != nil {
			return nil, err
		}
		snap.Runtimes[r.PaneID] = r
	}
	return snap, rt.Err()
}

// Audit appends an audit event (its Time is now).
func (s *Store) Audit(e AuditEvent) error {
	_, err := s.db.Exec(`INSERT INTO audit(ts, actor, action, target, detail, who, summary, kind) VALUES(?,?,?,?,?,?,?,?)`,
		time.Now().UnixMilli(), e.Actor, e.Action, e.Target, e.Detail, e.Who, e.Summary, e.Kind)
	if err == nil && s.audits.Add(1)%auditPruneEvery == 0 {
		// The event is in: failing to prune isn't failing to record it.
		if perr := s.PruneAudit(auditKeepRows, auditKeepFailed, auditKeepAge); perr != nil {
			slog.Warn("state: prune audit", "err", perr)
		}
	}
	return err
}

// failedActions are the audit events anyone who can reach this Mac can
// cause (failed sign-ins and joins): they're kept to a cap of their own,
// so they can't push the rest of the history out.
const failedActions = `('auth-failed','pair-rejected','join-rejected')`

// PruneAudit drops audit events older than maxAge, failed sign-ins and
// joins beyond the newest keepFailed of them, and the rest beyond the
// newest keep.
func (s *Store) PruneAudit(keep, keepFailed int, maxAge time.Duration) error {
	_, err := s.db.Exec(`DELETE FROM audit WHERE ts < ?
		OR (action IN `+failedActions+` AND id <= (SELECT id FROM audit WHERE action IN `+failedActions+` ORDER BY id DESC LIMIT 1 OFFSET ?))
		OR (action NOT IN `+failedActions+` AND id <= (SELECT id FROM audit WHERE action NOT IN `+failedActions+` ORDER BY id DESC LIMIT 1 OFFSET ?))`,
		time.Now().Add(-maxAge).UnixMilli(), keepFailed, keep)
	return err
}

// AuditEvent is one audit log entry.
type AuditEvent struct {
	Time                          time.Time
	Actor, Action, Target, Detail string
	// In words (empty in events from before 2026-11-02): who did it
	// ("Claude in Web ▸ api"), what ("typed in Web ▸ api ▸ pane 2"), and
	// the kind of event (agent, device, guest, you, security).
	Who, Summary, Kind string
}

// RecentAudit returns the latest n audit events, newest first.
func (s *Store) RecentAudit(n int) ([]AuditEvent, error) {
	rows, err := s.db.Query(`SELECT ts, actor, action, target, detail, who, summary, kind FROM audit ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var ms int64
		var e AuditEvent
		if err := rows.Scan(&ms, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.Who, &e.Summary, &e.Kind); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ms)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Device is a paired remote client.
type Device struct {
	ID, Name, Platform string
	PublicKey          []byte
	Paired, LastSeen   time.Time
}

// PutDevice upserts a device.
func (s *Store) PutDevice(d Device) error {
	_, err := s.db.Exec(`INSERT INTO devices(id,name,platform,pubkey,paired,last_seen) VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, last_seen=excluded.last_seen`,
		d.ID, d.Name, d.Platform, d.PublicKey, d.Paired.UnixMilli(), d.LastSeen.UnixMilli())
	return err
}

// Devices lists paired devices.
func (s *Store) Devices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT id,name,platform,pubkey,paired,last_seen FROM devices ORDER BY paired`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		var paired, seen int64
		if err := rows.Scan(&d.ID, &d.Name, &d.Platform, &d.PublicKey, &paired, &seen); err != nil {
			return nil, err
		}
		d.Paired, d.LastSeen = time.UnixMilli(paired), time.UnixMilli(seen)
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDevice removes a device.
func (s *Store) DeleteDevice(id string) error {
	_, err := s.db.Exec(`DELETE FROM devices WHERE id=?`, id)
	return err
}

// Setting reads a persisted setting ("" if unset).
func (s *Store) Setting(key string) string {
	var v string
	_ = s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v
}

// SetSetting persists a setting.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// PutRecord stores a protobuf record of an arbitrary kind (multiplayer
// people and peer links).
func (s *Store) PutRecord(kind, id string, m proto.Message) error { return s.put(kind, id, m) }

// DeleteRecord removes a record stored with PutRecord.
func (s *Store) DeleteRecord(kind, id string) error { return s.del(kind, id) }

// Records returns every record of a kind, keyed by id.
func (s *Store) Records(kind string) (map[string][]byte, error) {
	rows, err := s.db.Query(`SELECT id, data FROM entities WHERE kind=?`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		out[id] = data
	}
	return out, rows.Err()
}
