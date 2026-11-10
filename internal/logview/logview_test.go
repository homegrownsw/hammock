package logview

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func feed(t *testing.T, s *Store, text string) []*pb.LogEntry {
	t.Helper()
	f := &Feeder{Store: s}
	f.parse.Now = func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local) }
	f.Write([]byte(text), 0)
	f.Flush(true)
	es, _ := s.Query(nil, 0, 0, 1000)
	return es
}

func TestFormats(t *testing.T) {
	es := feed(t, NewStore(""), strings.Join([]string{
		"Timestamp               Ty Process[PID:TID]",
		"2026-11-01 13:40:09.375 Db Hammock[40392:b683f0] [com.apple.defaults:User Defaults] found no value",
		"2026-11-01 13:40:09.380 E  SimRenderServer[87356:b6df71] (IOSurface) client went away",
		"  with a second line",
		"11-01 13:31:23.203  1234  1250 W ActivityManager: Slow operation",
		"11-01 13:31:23.204  1234  1250 E AndroidRuntime: FATAL EXCEPTION: main",
		"11-01 13:31:23.204  1234  1250 E AndroidRuntime: java.lang.RuntimeException: boom",
		`{"time":"2026-11-01T12:00:01Z","level":"warn","msg":"disk low","free":"2GB"}`,
		`time=2026-11-01T12:00:02.000Z level=ERROR msg="request failed" path=/v1 status=500`,
		"2026-11-01 12:00:03 INFO server listening on :3000",
		"panic: runtime error: index out of range",
		"",
		"goroutine 1 [running]:",
		"main.main()",
		"\t/src/main.go:12 +0x1d",
		"\x1b[32mready\x1b[0m in 120ms",
	}, "\r\n")+"\r\n")
	type want struct {
		level        pb.LogLevel
		process, tag string
		msg          string
	}
	wants := []want{
		{pb.LogLevel_LOG_LEVEL_DEBUG, "Hammock", "com.apple.defaults", "found no value"},
		{pb.LogLevel_LOG_LEVEL_ERROR, "SimRenderServer", "", "(IOSurface) client went away\n  with a second line"},
		{pb.LogLevel_LOG_LEVEL_WARNING, "", "ActivityManager", "Slow operation"},
		{pb.LogLevel_LOG_LEVEL_ERROR, "", "AndroidRuntime", "FATAL EXCEPTION: main\njava.lang.RuntimeException: boom"},
		{pb.LogLevel_LOG_LEVEL_WARNING, "", "", "disk low"},
		{pb.LogLevel_LOG_LEVEL_ERROR, "", "", "request failed"},
		{pb.LogLevel_LOG_LEVEL_INFO, "", "", "server listening on :3000"},
		{pb.LogLevel_LOG_LEVEL_ERROR, "", "", "panic: runtime error: index out of range\n\ngoroutine 1 [running]:\nmain.main()\n\t/src/main.go:12 +0x1d"},
		{pb.LogLevel_LOG_LEVEL_INFO, "", "", "ready in 120ms"},
	}
	if len(es) != len(wants) {
		for _, e := range es {
			t.Logf("%v %q %q %q", e.Level, e.Process, e.Tag, e.Message)
		}
		t.Fatalf("%d entries, want %d", len(es), len(wants))
	}
	for i, w := range wants {
		e := es[i]
		if e.Level != w.level || e.Process != w.process || e.Tag != w.tag || e.Message != w.msg {
			t.Errorf("entry %d: %v %q %q %q, want %v %q %q %q", i, e.Level, e.Process, e.Tag, e.Message, w.level, w.process, w.tag, w.msg)
		}
	}
	if es[4].Fields["free"] != "2GB" || es[5].Fields["status"] != "500" || es[5].Fields["path"] != "/v1" {
		t.Errorf("fields: %v %v", es[4].Fields, es[5].Fields)
	}
	if got := time.UnixMilli(es[4].TimeUnixMs).UTC(); !got.Equal(time.Date(2026, 11, 1, 12, 0, 1, 0, time.UTC)) {
		t.Errorf("json time %v", got)
	}
}

func TestFilterAndFacets(t *testing.T) {
	s := NewStore("")
	feed(t, s, "2026-11-01 13:40:09.375 Db Hammock[1:a] [com.x:y] hello world\n"+
		"2026-11-01 13:40:09.376 E  Hammock[1:a] [com.x:y] it broke\n"+
		"2026-11-01 13:40:09.377 Df Safari[2:b] loading page\n")
	q := func(f *pb.LogFilter) (msgs []string) {
		es, _ := s.Query(NewMatcher(f), 0, 0, 100)
		for _, e := range es {
			msgs = append(msgs, e.Message)
		}
		return msgs
	}
	if got := q(&pb.LogFilter{Text: "WORLD"}); len(got) != 1 || got[0] != "hello world" {
		t.Errorf("text: %v", got)
	}
	if got := q(&pb.LogFilter{Text: "/br.ke|page/"}); len(got) != 2 {
		t.Errorf("regex: %v", got)
	}
	if got := q(&pb.LogFilter{Levels: []pb.LogLevel{pb.LogLevel_LOG_LEVEL_ERROR}}); len(got) != 1 || got[0] != "it broke" {
		t.Errorf("level: %v", got)
	}
	if got := q(&pb.LogFilter{Only: []string{"process:Safari"}}); len(got) != 1 {
		t.Errorf("only: %v", got)
	}
	if got := q(&pb.LogFilter{Hide: []string{"tag:com.x"}}); len(got) != 1 || got[0] != "loading page" {
		t.Errorf("hide: %v", got)
	}
	facets := map[string]int64{}
	for _, f := range s.Facets(10) {
		facets[f.Key] = f.Count
	}
	if facets["process:Hammock"] != 2 || facets["process:Safari"] != 1 || facets["level:error"] != 1 || facets["tag:com.x"] != 2 {
		t.Errorf("facets: %v", facets)
	}
	// Paging: the newest 1, then the one before it.
	es, more := s.Query(nil, 0, 0, 1)
	if len(es) != 1 || !more || es[0].Message != "loading page" {
		t.Fatalf("page: %v %v", es, more)
	}
	es, _ = s.Query(nil, es[0].Seq, 0, 1)
	if len(es) != 1 || es[0].Message != "it broke" {
		t.Fatalf("older: %v", es)
	}
	es, _ = s.Query(nil, 0, es[0].Seq, 10)
	if len(es) != 1 || es[0].Message != "loading page" {
		t.Fatalf("after: %v", es)
	}
}

// Entries survive in their file, and output replayed after a restart
// isn't counted twice.
func TestStoreFileAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.log")
	s := NewStore(path)
	f := &Feeder{Store: s}
	out := []byte("one\ntwo\nthree\n")
	f.Write(out, 0)
	f.Flush(false)
	s.Close()

	s2 := NewStore(path)
	if s2.Len() != 3 || s2.Offset() != uint64(len(out)) {
		t.Fatalf("loaded %d entries to offset %d", s2.Len(), s2.Offset())
	}
	f2 := &Feeder{Store: s2}
	f2.Write(append(out, "four\n"...), 0) // the holder replays from earlier
	f2.Flush(false)
	es, _ := s2.Query(nil, 0, 0, 10)
	var msgs []string
	for _, e := range es {
		msgs = append(msgs, e.Message)
	}
	if strings.Join(msgs, ",") != "one,two,three,four" {
		t.Fatalf("after replay: %v", msgs)
	}
	if es[3].Seq != 4 {
		t.Fatalf("numbering went on from %d", es[3].Seq)
	}
}

func TestTrim(t *testing.T) {
	s := NewStore("")
	s.max = 10
	for i := 0; i < 30; i++ {
		s.Add(&pb.LogEntry{Message: "x", Process: "p"}, 0)
	}
	if s.Len() > 13 || s.Len() < 10 {
		t.Fatalf("held %d", s.Len())
	}
	if c := s.counts["process:p"]; c != int64(s.Len()) {
		t.Fatalf("count %d for %d entries", c, s.Len())
	}
}
