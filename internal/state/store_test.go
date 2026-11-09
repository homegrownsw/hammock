package state

import (
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
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
