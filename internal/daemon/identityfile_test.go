package daemon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/homegrownsw/hammock/internal/state"
)

// identityDaemon is just enough of a daemon for loadIdentity, in dir.
func identityDaemon(t *testing.T, dir string) *Daemon {
	t.Helper()
	old, oldWait := computerName, readRetryWait
	computerName = func() string { return "Studio" }
	readRetryWait = 0
	t.Cleanup(func() { computerName, readRetryWait = old, oldWait })
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Daemon{cfg: Config{Dir: dir}, store: st}
}

// files lists what's in dir, but the state database's.
func files(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		if n := e.Name(); !slices.Contains([]string{"state.db", "state.db-wal", "state.db-shm"}, n) {
			out = append(out, n)
		}
	}
	return out
}

// The identity key is made when it doesn't exist, and loaded unchanged
// after: the same user id each time.
func TestIdentityKeyMadeOnceThenKept(t *testing.T) {
	dir := t.TempDir()
	d := identityDaemon(t, dir)
	if err := d.loadIdentity(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "identity.key")
	st, err := os.Stat(path)
	if err != nil || st.Size() != 32 || st.Mode().Perm() != 0o600 {
		t.Fatalf("identity.key: %v %v", st, err)
	}
	if got := files(t, dir); !slices.Equal(got, []string{"identity.key"}) {
		t.Fatalf("left behind: %v", got)
	}
	seed, _ := os.ReadFile(path)
	id := d.id.me.UserId
	d2 := identityDaemon(t, dir)
	if err := d2.loadIdentity(); err != nil {
		t.Fatal(err)
	}
	if d2.id.me.UserId != id {
		t.Fatalf("user id changed: %s, then %s", id, d2.id.me.UserId)
	}
	if again, _ := os.ReadFile(path); !bytes.Equal(again, seed) {
		t.Fatal("identity.key was rewritten")
	}
}

// An identity key that can't be read, or isn't one, is an error, and is
// left as it is: never replaced by a new identity.
func TestIdentityKeyDamagedIsKept(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"empty", nil, 0o600},
		{"truncated", bytes.Repeat([]byte{7}, 31), 0o600},
		{"too long", bytes.Repeat([]byte{7}, 33), 0o600},
		{"unreadable", bytes.Repeat([]byte{7}, 32), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mode == 0 && os.Geteuid() == 0 {
				t.Skip("root reads anything")
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "identity.key")
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(path, 0o600) })
			d := identityDaemon(t, dir)
			if err := d.loadIdentity(); err == nil {
				t.Fatalf("loaded: %s", d.id.me.GetUserId())
			} else {
				t.Log(err)
			}
			if d.id.me != nil {
				t.Fatal("an identity was set")
			}
			os.Chmod(path, 0o600)
			if got, _ := os.ReadFile(path); !bytes.Equal(got, tc.data) {
				t.Fatalf("identity.key changed: %x", got)
			}
			if got := files(t, dir); !slices.Equal(got, []string{"identity.key"}) {
				t.Fatalf("files: %v", got)
			}
		})
	}
}

func noRetryWait(t *testing.T) {
	old := readRetryWait
	readRetryWait = 0
	t.Cleanup(func() { readRetryWait = old })
}

// The certificate is made when neither file exists, and loaded unchanged after.
func TestCertMadeOnceThenKept(t *testing.T) {
	noRetryWait(t)
	dir := t.TempDir()
	_, fp, err := loadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	tlsDir := filepath.Join(dir, "tls")
	if got := files(t, tlsDir); !slices.Equal(got, []string{"cert.pem", "key.pem"}) {
		t.Fatalf("tls: %v", got)
	}
	for _, f := range []string{"cert.pem", "key.pem"} {
		if st, err := os.Stat(filepath.Join(tlsDir, f)); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", f, st, err)
		}
	}
	_, fp2, err := loadOrCreateCert(dir)
	if err != nil || fp2 != fp {
		t.Fatalf("loaded again: %s (was %s), %v", fp2, fp, err)
	}
}

// A certificate with one of its files missing, or either unreadable or
// unparsable, is an error, and the files are left as they are: no new
// certificate.
func TestCertDamagedIsKept(t *testing.T) {
	noRetryWait(t)
	for _, tc := range []struct {
		name   string
		break_ func(cert, key string)
	}{
		{"cert missing", func(cert, key string) { os.Remove(cert) }},
		{"key missing", func(cert, key string) { os.Remove(key) }},
		{"cert truncated", func(cert, key string) {
			b, _ := os.ReadFile(cert)
			os.WriteFile(cert, b[:len(b)/2], 0o600)
		}},
		{"key empty", func(cert, key string) { os.WriteFile(key, nil, 0o600) }},
		{"key unreadable", func(cert, key string) { os.Chmod(key, 0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "key unreadable" && os.Geteuid() == 0 {
				t.Skip("root reads anything")
			}
			dir := t.TempDir()
			if _, _, err := loadOrCreateCert(dir); err != nil {
				t.Fatal(err)
			}
			tlsDir := filepath.Join(dir, "tls")
			cert, key := filepath.Join(tlsDir, "cert.pem"), filepath.Join(tlsDir, "key.pem")
			tc.break_(cert, key)
			t.Cleanup(func() { os.Chmod(key, 0o600) })
			before := snapshot(t, tlsDir)
			if _, fp, err := loadOrCreateCert(dir); err == nil {
				t.Fatalf("loaded: %s", fp)
			} else {
				t.Log(err)
			}
			if after := snapshot(t, tlsDir); !mapsEqual(before, after) {
				t.Fatalf("tls changed: %v, then %v", before, after)
			}
		})
	}
}

// snapshot is each file in dir, by name: its mode and contents.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, n := range files(t, dir) {
		p := filepath.Join(dir, n)
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		os.Chmod(p, 0o600)
		b, _ := os.ReadFile(p)
		os.Chmod(p, st.Mode().Perm())
		out[n] = st.Mode().Perm().String() + " " + string(b)
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// A failed write leaves no file, whole or partial, and no temporary one: a
// set of files is all there or none.
func TestWriteFilesAtomicFailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	old := syncFile
	syncFile = func(*os.File) error { return errors.New("disk full") }
	err := writeFilesAtomic(privateFile{filepath.Join(dir, "a"), []byte("a")}, privateFile{filepath.Join(dir, "b"), []byte("b")})
	syncFile = old
	if err == nil {
		t.Fatal("no error")
	}
	if got := files(t, dir); len(got) != 0 {
		t.Fatalf("left behind: %v", got)
	}
	// The second can't be renamed into place (its folder is gone): the
	// first, already there, is taken away again.
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	syncFile = func(f *os.File) error {
		if filepath.Base(f.Name()) != "a" && filepath.Dir(f.Name()) == sub {
			os.RemoveAll(sub) // after b's temporary file is written
		}
		return f.Sync()
	}
	t.Cleanup(func() { syncFile = old })
	err = writeFilesAtomic(privateFile{filepath.Join(dir, "a"), []byte("a")}, privateFile{filepath.Join(sub, "b"), []byte("b")})
	if err == nil {
		t.Fatal("no error")
	}
	if got := files(t, dir); len(got) != 0 {
		t.Fatalf("left behind: %v", got)
	}
}
