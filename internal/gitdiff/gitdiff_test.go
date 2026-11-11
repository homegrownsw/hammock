package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Sam", "GIT_AUTHOR_EMAIL=sam@example.com",
		"GIT_COMMITTER_NAME=Sam", "GIT_COMMITTER_EMAIL=sam@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo makes a repository on main with two commits, then a feature branch
// with one more.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	run(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "one\ntwo\nthree\n")
	write(t, dir, "old.txt", "moving\nalong\nnicely\nhere\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "First")
	write(t, dir, "b.txt", "bee\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "Second")
	run(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "one\n2\nthree\nfour\n")
	run(t, dir, "mv", "old.txt", "new.txt")
	run(t, dir, "commit", "-q", "-am", "Feature work")
	return dir
}

func byPath(files []*pb.DiffFile) map[string]*pb.DiffFile {
	m := map[string]*pb.DiffFile{}
	for _, f := range files {
		m[f.Path] = f
	}
	return m
}

func TestBranchByDefault(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	write(t, dir, "untracked.txt", "x\ny\n")
	r, err := Resolve(ctx, filepath.Join(dir), &pb.DiffSource{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Source.Kind != pb.DiffSource_KIND_BRANCH || !r.Source.WithUncommitted || r.Source.Base != "main" {
		t.Fatalf("source = %v", r.Source)
	}
	if r.Title != "feature vs main, with uncommitted changes" || !r.Worktree() {
		t.Fatalf("title %q, head %q", r.Title, r.Head)
	}
	files, err := Files(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(files)
	if a := m["a.txt"]; a == nil || a.Status != pb.DiffFile_STATUS_MODIFIED || a.Added != 2 || a.Removed != 1 {
		t.Errorf("a.txt = %v", a)
	}
	if n := m["new.txt"]; n == nil || n.Status != pb.DiffFile_STATUS_RENAMED || n.OldPath != "old.txt" {
		t.Errorf("new.txt = %v", n)
	}
	if u := m["untracked.txt"]; u == nil || !u.Untracked || u.Added != 2 {
		t.Errorf("untracked.txt = %v", u)
	}
	if m["b.txt"] != nil {
		t.Error("b.txt is on main too, so not in the branch's diff")
	}
	text, err := FileText(ctx, r, m["a.txt"])
	if err != nil {
		t.Fatal(err)
	}
	if text.OldText != "one\ntwo\nthree\n" || text.NewText != "one\n2\nthree\nfour\n" {
		t.Errorf("a.txt sides %q / %q", text.OldText, text.NewText)
	}
	text, err = FileText(ctx, r, m["new.txt"])
	if err != nil || text.OldText != "moving\nalong\nnicely\nhere\n" || text.NewText != text.OldText {
		t.Errorf("renamed sides %q / %q (%v)", text.OldText, text.NewText, err)
	}
}

func TestUncommittedOnMain(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	run(t, dir, "checkout", "-q", "main")
	r, err := Resolve(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source.Kind != pb.DiffSource_KIND_UNCOMMITTED {
		t.Fatalf("on the default branch: %v", r.Source.Kind)
	}
	before, _ := Fingerprint(ctx, r)
	files, _ := Files(ctx, r)
	if len(files) != 0 {
		t.Fatalf("nothing changed yet: %v", files)
	}
	write(t, dir, "b.txt", "bee\nbuzz\n")
	run(t, dir, "add", "b.txt") // staged counts too
	files, _ = Files(ctx, r)
	if len(files) != 1 || files[0].Path != "b.txt" || files[0].Added != 1 {
		t.Fatalf("files = %v", files)
	}
	mid, _ := Fingerprint(ctx, r)
	if mid == before {
		t.Error("the fingerprint didn't change with the file")
	}
	write(t, dir, "b.txt", "bee\nbuzz\nmore\n")
	if after, _ := Fingerprint(ctx, r); after == mid {
		t.Error("editing an already changed file left the fingerprint the same")
	}
}

func TestCommitAndRange(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	r, err := Resolve(ctx, dir, ParseRev("HEAD~1"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Source.Kind != pb.DiffSource_KIND_COMMIT || !strings.HasSuffix(r.Title, " Second") {
		t.Fatalf("%v %q", r.Source, r.Title)
	}
	files, _ := Files(ctx, r)
	if len(files) != 1 || files[0].Path != "b.txt" || files[0].Status != pb.DiffFile_STATUS_ADDED {
		t.Fatalf("files = %v", files)
	}
	root, err := Resolve(ctx, dir, ParseRev("main~1"))
	if err != nil || root.Base != EmptyTree {
		t.Fatalf("a root commit compares with nothing: %v %v", root, err)
	}
	files, _ = Files(ctx, root)
	if len(files) != 2 {
		t.Fatalf("root files = %v", files)
	}
	rg, err := Resolve(ctx, dir, ParseRev("main..feature"))
	if err != nil {
		t.Fatal(err)
	}
	if rg.Title != "main..feature" || rg.Worktree() || rg.Live() {
		t.Fatalf("%q %v", rg.Title, rg.Head)
	}
	if _, err := Resolve(ctx, dir, ParseRev("nope")); err == nil {
		t.Error("no such commit")
	}
}

func TestBinaryAndGenerated(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	png := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
	write(t, dir, "pic.png", png)
	write(t, dir, "go.sum", "x\n")
	write(t, dir, "gen/out.txt", "made\n")
	write(t, dir, ".gitattributes", "gen/** linguist-generated\n")
	r, _ := Resolve(ctx, dir, &pb.DiffSource{Kind: pb.DiffSource_KIND_UNCOMMITTED})
	files, err := Files(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(files)
	if p := m["pic.png"]; p == nil || !p.Binary {
		t.Fatalf("pic.png = %v", p)
	}
	if !m["go.sum"].GetGenerated() || !m["gen/out.txt"].GetGenerated() || m[".gitattributes"].GetGenerated() {
		t.Errorf("generated: go.sum %v, gen/out.txt %v", m["go.sum"], m["gen/out.txt"])
	}
	text, err := FileText(ctx, r, m["pic.png"])
	if err != nil || text.Mime != "image/png" || string(text.NewData) != png || text.OldData != nil {
		t.Errorf("picture: %v %v", text, err)
	}
}

func TestNotARepository(t *testing.T) {
	if _, err := Resolve(context.Background(), t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "isn't in a git repository") {
		t.Fatalf("err = %v", err)
	}
}

func TestGoneOrNot(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	if _, err := Resolve(ctx, dir, ParseRev("0123456789abcdef0123456789abcdef01234567")); !IsGone(err) {
		t.Errorf("a commit that isn't there: %v", err)
	}
	if _, err := Resolve(ctx, dir, &pb.DiffSource{Kind: pb.DiffSource_KIND_BRANCH, Base: "nope"}); !IsGone(err) {
		t.Errorf("a branch that isn't there: %v", err)
	}
	if _, err := Resolve(ctx, filepath.Join(dir, "missing"), nil); err == nil || IsGone(err) {
		t.Errorf("a folder that isn't there isn't gone for good: %v", err)
	}
}
