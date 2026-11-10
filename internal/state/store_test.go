package state

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	if err := s.PruneAudit(4, 4, 4, time.Hour); err != nil {
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
	if err := s.PruneAudit(5, 4, 4, time.Hour); err != nil {
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

// Refused attempts have a cap of their own too, apart from failed sign-ins:
// a flood of them pushes out neither the real history nor the failures.
func TestPruneAuditRefusedSeparately(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, a := range []string{"paired", "close-pane", "auth-failed", "join-rejected"} {
		if err := s.Audit(AuditEvent{Actor: "user", Action: a}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 30 {
		if err := s.Audit(AuditEvent{Actor: "guest:us_x", Action: RefusedPrefix + []string{"close-pane", "create-tab", "more"}[i%3]}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PruneAudit(5, 4, 6, time.Hour); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.RecentAudit(100)
	var real, failed, refused int
	for _, e := range evs {
		switch {
		case strings.HasPrefix(e.Action, RefusedPrefix):
			refused++
		case e.Action == "auth-failed" || e.Action == "join-rejected":
			failed++
		default:
			real++
		}
	}
	if real != 2 || failed != 2 || refused != 6 {
		t.Fatalf("kept %d others, %d failures and %d refused, want 2, 2 and 6", real, failed, refused)
	}
}

// Every field of an audit event is kept within its limit, on one line,
// cut on a character boundary, whatever the caller gives.
func TestAuditFieldsBounded(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	huge := strings.Repeat("é", 300000) // two bytes each: a cut can land mid-character
	e := AuditEvent{Actor: "peer:" + huge, Action: huge, Target: "gr_1 → /" + huge, Detail: "line one\nline two\x1b[2J‮" + huge,
		Who: huge, Summary: huge, Kind: huge}
	if err := s.Audit(e); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.RecentAudit(1)
	got := evs[0]
	for _, f := range []struct {
		name, v string
		max     int
	}{
		{"actor", got.Actor, AuditActorMax}, {"action", got.Action, AuditActionMax}, {"target", got.Target, AuditTargetMax},
		{"detail", got.Detail, AuditDetailMax}, {"who", got.Who, AuditWhoMax}, {"summary", got.Summary, AuditSummaryMax}, {"kind", got.Kind, AuditKindMax},
	} {
		if len(f.v) > f.max || !utf8.ValidString(f.v) || !strings.HasSuffix(f.v, "…") {
			t.Errorf("%s: %d bytes (at most %d), valid %v, %q…", f.name, len(f.v), f.max, utf8.ValidString(f.v), f.v[:min(len(f.v), 20)])
		}
	}
	if strings.ContainsAny(got.Detail, "\n\x1b‮") || !strings.HasPrefix(got.Detail, "line one line two [2J ") {
		t.Errorf("detail kept its controls: %q", got.Detail[:40])
	}
	// What's within its limits is kept as it is.
	if AuditText("Claude in Web ▸ api", AuditWhoMax) != "Claude in Web ▸ api" {
		t.Error("a short field was changed")
	}
	if c := AuditText(strings.Repeat("x", 500), AuditTargetMax); AuditText(c, AuditTargetMax) != c {
		t.Error("cutting a field twice changes it again")
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
