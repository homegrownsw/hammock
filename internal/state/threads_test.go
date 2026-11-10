package state

import (
	"path/filepath"
	"testing"

	"github.com/homegrownsw/hammock/internal/thread"
)

func TestThreadRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	th := thread.Thread{ID: thread.NewID(), Harness: "claude", NativeID: "abc", Cwd: "/tmp"}
	recs := []thread.Record{{Kind: thread.KindUser, Text: "hi"}, {Kind: thread.KindAssistant, Text: "yo"}}
	thread.Seal(recs)
	if err := st.PutThread(th, recs); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.ThreadByNative("claude", "abc")
	if err != nil || !ok || got.ID != th.ID {
		t.Fatalf("by native: %v %v %v", got, ok, err)
	}
	_, rs, err := st.LoadThread(th.ID)
	if err != nil || len(rs) != 2 || rs[1].Text != "yo" || rs[1].Hash != recs[1].Hash {
		t.Fatalf("load: %v %v", rs, err)
	}
}
