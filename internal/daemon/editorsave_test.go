package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

func saveTestDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_, ino := fileID(st)
	return ino
}

func wantFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", filepath.Base(path), got, want)
	}
}

// onlyFiles fails if dir holds anything but names (a temp file or backup
// left behind).
func onlyFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	es, _ := os.ReadDir(dir)
	var got []string
	for _, e := range es {
		got = append(got, e.Name())
	}
	if !slices.Equal(got, names) {
		t.Fatalf("%s holds %v, want %v", dir, got, names)
	}
}

func TestSaveFileReplacesAndKeepsMode(t *testing.T) {
	dir := saveTestDir(t)
	for _, mode := range []os.FileMode{0o640, 0o755} {
		p := filepath.Join(dir, "f")
		os.Remove(p)
		if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		os.Chmod(p, mode)
		before := inode(t, p)
		if err := saveFile(p, []byte("new"), 0o644); err != nil {
			t.Fatal(err)
		}
		wantFile(t, p, "new")
		st, _ := os.Stat(p)
		if st.Mode().Perm() != mode {
			t.Fatalf("mode %v, want %v", st.Mode().Perm(), mode)
		}
		if inode(t, p) == before {
			t.Fatal("saved in place: want a new file renamed over it")
		}
		onlyFiles(t, dir, "f")
	}
	// A new file gets the mode asked for.
	n := filepath.Join(dir, "new")
	if err := saveFile(n, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(n); st.Mode().Perm() != 0o640 {
		t.Fatalf("new file mode %v", st.Mode().Perm())
	}
}

func TestSaveFileKeepsHardLinks(t *testing.T) {
	dir := saveTestDir(t)
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	ino := inode(t, a)
	if err := saveFile(a, []byte("new text"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantFile(t, a, "new text")
	wantFile(t, b, "new text")
	if inode(t, a) != ino || inode(t, b) != ino {
		t.Fatal("the links no longer share a file")
	}
	onlyFiles(t, dir, "a", "b")
}

func TestSaveFileKeepsXattrs(t *testing.T) {
	dir := saveTestDir(t)
	p := filepath.Join(dir, "tagged")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, val := "user.hammock-test", []byte("kept")
	if runtime.GOOS == "darwin" {
		// A Finder tag (Red).
		name = "com.apple.metadata:_kMDItemUserTags"
		val = []byte("bplist00\xa1\x01URed\n6\x08\x0a\x00\x00\x00\x00\x00\x00\x01\x01\x00\x00\x00\x00\x00\x00\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x10")
	}
	if err := unix.Setxattr(p, name, val, 0); err != nil {
		t.Skipf("no xattrs here: %v", err)
	}
	before := inode(t, p)
	if err := saveFile(p, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantFile(t, p, "new")
	if inode(t, p) == before {
		t.Fatal("saved in place: want the attributes copied to the new file")
	}
	got, err := xattrOf(p, name)
	if err != nil {
		t.Fatalf("%s gone: %v", name, err)
	}
	if !bytes.Equal(got, val) {
		t.Fatalf("%s = %q, want %q", name, got, val)
	}
}

func xattrOf(path, name string) ([]byte, error) {
	buf := make([]byte, 4096)
	n, err := unix.Getxattr(path, name, buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func TestSaveFileInReadOnlyFolder(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anywhere")
	}
	tmp := saveTestDir(t)
	t.Setenv("TMPDIR", tmp) // where the backup goes
	dir := filepath.Join(saveTestDir(t), "ro")
	os.Mkdir(dir, 0o755)
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("old and longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	ino := inode(t, p)
	if err := saveFile(p, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantFile(t, p, "new")
	if inode(t, p) != ino {
		t.Fatal("not saved in place")
	}
	onlyFiles(t, dir, "f")
	onlyFiles(t, tmp) // the backup, removed
}

func TestSaveFileThroughSymlink(t *testing.T) {
	dir := saveTestDir(t)
	real, link := filepath.Join(dir, "real"), filepath.Join(dir, "link")
	if err := os.WriteFile(real, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", link); err != nil {
		t.Fatal(err)
	}
	// As the editor does: the path resolved when the file's opened, and
	// saveFile given the link itself.
	resolved, err := resolveEditorPath(link, "")
	if err != nil || resolved != real {
		t.Fatalf("resolved %q, %v", resolved, err)
	}
	for i, p := range []string{resolved, link} {
		text := []string{"one", "two"}[i]
		if err := saveFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		st, err := os.Lstat(link)
		if err != nil || st.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("link is no longer a symlink (%v)", err)
		}
		wantFile(t, real, text)
	}
	onlyFiles(t, dir, "link", "real")
}

func TestSaveInPlaceDecision(t *testing.T) {
	for _, c := range []struct {
		nlink uint64
		owner uint32
		uid   int
		want  bool
	}{
		{1, 501, 501, false}, // ours, one link: replace it
		{2, 501, 501, true},  // hard linked
		{1, 0, 501, true},    // someone else's, writable to us
		{3, 0, 501, true},
		{1, 0, 0, false},
	} {
		if got := saveInPlace(c.nlink, c.owner, c.uid); got != c.want {
			t.Errorf("saveInPlace(%d, %d, %d) = %v, want %v", c.nlink, c.owner, c.uid, got, c.want)
		}
	}
}
