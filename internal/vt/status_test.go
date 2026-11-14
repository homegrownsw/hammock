package vt

import "testing"

func report(st StatusState, id string) StatusReport {
	return StatusReport{State: st, ID: id, Progress: -1}
}

func TestStatusRecordsReplaceWhole(t *testing.T) {
	var r StatusRecords
	r.Apply(StatusReport{State: StatusWorking, App: "brew", Title: "x", Progress: 40})
	r.Apply(report(StatusDone, ""))
	got, _ := r.Root()
	if got.App != "" || got.Title != "" || got.Progress != -1 {
		t.Fatalf("a report replaces its record whole: %+v", got)
	}
	if r.Apply(report(StatusDone, "")) {
		t.Fatal("the same report again isn't a change")
	}
}

func TestStatusRecordsClearSubtree(t *testing.T) {
	var r StatusRecords
	for _, id := range []string{"", "build", "build/test", "builder", "deploy"} {
		r.Apply(report(StatusWorking, id))
	}
	r.Apply(report(StatusClear, "build"))
	var ids []string
	for _, x := range r.Records() {
		ids = append(ids, x.ID)
	}
	if want := []string{"", "builder", "deploy"}; len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("after clearing build: %q", ids)
	}
	r.Apply(report(StatusClear, ""))
	if r.Len() != 0 {
		t.Fatal("clearing the root clears every record")
	}
}

func TestStatusRecordsInheritApp(t *testing.T) {
	var r StatusRecords
	r.Apply(StatusReport{State: StatusWorking, App: "deploy", Progress: -1})
	r.Apply(StatusReport{State: StatusBlocked, ID: "eu-west/db", Kind: "permission", Progress: -1})
	top, ok := r.Top()
	if !ok || top.ID != "eu-west/db" || top.App != "deploy" || top.Kind != "permission" {
		t.Fatalf("top: %+v", top)
	}
}

func TestStatusRecordsTopRanks(t *testing.T) {
	var r StatusRecords
	r.Apply(report(StatusWorking, ""))
	r.Apply(report(StatusDone, "a"))
	r.Apply(report(StatusIdle, "b"))
	if top, _ := r.Top(); top.ID != "a" {
		t.Fatalf("done outranks working: %+v", top)
	}
	r.Apply(report(StatusError, "c"))
	if top, _ := r.Top(); top.ID != "c" {
		t.Fatalf("error outranks done: %+v", top)
	}
	r.Apply(report(StatusBlocked, "d"))
	if top, _ := r.Top(); top.ID != "d" {
		t.Fatalf("blocked outranks all: %+v", top)
	}
	r.Apply(report(StatusBlocked, ""))
	if top, _ := r.Top(); top.ID != "" {
		t.Fatalf("among equals the root: %+v", top)
	}
	r.Apply(report(StatusClear, ""))
	r.Apply(report(StatusDone, ""))
	r.Apply(report(StatusWorking, "x"))
	r.Seen()
	if top, _ := r.Top(); top.ID != "x" {
		t.Fatalf("done, once seen, rests below working: %+v", top)
	}
}

func TestStatusRecordsEnded(t *testing.T) {
	var r StatusRecords
	r.Apply(report(StatusWorking, "w"))
	r.Apply(report(StatusBlocked, "b"))
	r.Apply(report(StatusIdle, "i"))
	r.Apply(report(StatusDone, "d"))
	r.Apply(report(StatusError, "e"))
	r.Ended()
	recs := r.Records()
	if len(recs) != 2 || recs[0].ID != "d" || recs[1].ID != "e" {
		t.Fatalf("done and error outlive the program: %+v", recs)
	}
	r.Seen()
	r.Ended()
	if r.Len() != 0 {
		t.Fatal("once seen, they go with the next prompt")
	}
}

func TestStatusRecordsCap(t *testing.T) {
	var r StatusRecords
	for i := range MaxStatusRecords + 5 {
		r.Apply(report(StatusWorking, string(rune('A'+i%26))+string(rune('a'+i/26))))
	}
	if r.Len() != MaxStatusRecords {
		t.Fatalf("records: %d", r.Len())
	}
	if _, ok := r.recs["Aa"]; ok {
		t.Fatal("the least recently updated goes first")
	}
}

func TestStatusRecordsNormalises(t *testing.T) {
	var r StatusRecords
	r.Apply(StatusReport{State: StatusDone, Kind: "auth", Progress: 50, Message: "ok‮gnp.exe​  now"})
	got, _ := r.Root()
	if got.Kind != "" || got.Progress != -1 {
		t.Fatalf("kind and progress only where they apply: %+v", got)
	}
	if got.Message != "okgnp.exe now" {
		t.Fatalf("invisible formatting removed: %q", got.Message)
	}
}
