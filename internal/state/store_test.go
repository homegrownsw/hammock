package state

import (
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Records stored before Spaces were renamed Groups load as Groups.
func TestSpacesMigrateToGroups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := proto.Marshal(&pb.Group{Id: "sp_old", Name: "Media", Icon: "📺"})
	if _, err := s.db.Exec(`INSERT INTO entities(kind,id,data) VALUES('space','sp_old',?)`, b); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Groups) != 1 || snap.Groups[0].Name != "Media" || snap.Groups[0].Icon != "📺" {
		t.Fatalf("groups after migration: %v", snap.Groups)
	}
}

// A runtime table from before holders had tokens gains the column; its
// records load with no token.
func TestRuntimeTokenMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE pane_runtime; CREATE TABLE pane_runtime (pane_id TEXT PRIMARY KEY, socket TEXT NOT NULL);
		INSERT INTO pane_runtime VALUES('pa_old','/x/old.sock')`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.PutRuntime(PaneRuntime{PaneID: "pa_new", Socket: "/x/new.sock", Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if r := snap.Runtimes["pa_old"]; r.Socket != "/x/old.sock" || r.Token != "" {
		t.Fatalf("old: %+v", r)
	}
	if r := snap.Runtimes["pa_new"]; r.Token != "tok" {
		t.Fatalf("new: %+v", r)
	}
}

// The audit log keeps only the newest events, and none too old.
func TestPruneAudit(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for range 10 {
		if err := s.Audit(AuditEvent{Actor: "a", Action: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE audit SET ts = 1 WHERE id = (SELECT MAX(id) FROM audit)`); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneAudit(4, 4, time.Hour); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.RecentAudit(100)
	if len(evs) != 3 {
		t.Fatalf("kept %d events, want the newest 4 less the old one", len(evs))
	}
}

// Failed sign-ins and joins are kept to a cap of their own: however many
// there are, they don't push the rest of the history out.
func TestPruneAuditFailuresSeparately(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for range 3 {
		if err := s.Audit(AuditEvent{Actor: "user", Action: "paired"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 20 {
		if err := s.Audit(AuditEvent{Actor: "remote", Action: []string{"auth-failed", "pair-rejected", "join-rejected"}[i%3]}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PruneAudit(5, 4, time.Hour); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.RecentAudit(100)
	var real, failed int
	for _, e := range evs {
		if e.Action == "paired" {
			real++
		} else {
			failed++
		}
	}
	if real != 3 || failed != 4 {
		t.Fatalf("kept %d others and %d failures, want 3 and 4", real, failed)
	}
}

// A store whose audit log can't be pruned (the disk is full, say) still
// opens, and still records events.
func TestPruneFailureIsntFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO audit(ts, actor, action, target, detail) VALUES(1,'a','x','','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER no_prune BEFORE DELETE ON audit BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatalf("open with a prune failing: %v", err)
	}
	defer s.Close()
	for i := range auditPruneEvery {
		if err := s.Audit(AuditEvent{Actor: "a", Action: "y"}); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
}
