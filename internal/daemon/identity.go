package daemon

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Multiplayer identity (ADR-0008): each Mac's daemon has one Ed25519 user
// key. Its id is derived from the public key; others know you by it.

type identity struct {
	key ed25519.PrivateKey
	me  *pb.Person
}

func userIDFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "us_" + hex.EncodeToString(sum[:8])
}

// A user id is only 64 bits of its key's hash, so two keys could share one
// (made to, with enough work). Wherever an id has a key on record here (a
// linked computer's, a person's as first seen signing in), the key itself
// must match: the id alone isn't trusted.

func personKeySetting(uid string) string { return "personkey." + uid }

// knownKey is the user key uid is known by here: a linked computer's, else
// a person's. nil: none on record (yet). Callers hold d.mu.
func (d *Daemon) knownKey(uid string) []byte {
	if l := d.comp.links[uid]; l != nil && len(l.GetRecord().GetKey()) == ed25519.PublicKeySize {
		return l.Record.Key
	}
	if k, err := hex.DecodeString(d.store.Setting(personKeySetting(uid))); err == nil && len(k) == ed25519.PublicKeySize {
		return k
	}
	return nil
}

// keyMatches: key is the one uid is known by here, or none is on record.
// Callers hold d.mu.
func (d *Daemon) keyMatches(uid string, key []byte) bool {
	k := d.knownKey(uid)
	return k == nil || bytes.Equal(k, key)
}

// rememberKey records the key a person (a guest, a linked computer) signed
// in with, the first time: from then on only that key is them. Callers hold d.mu.
func (d *Daemon) rememberKey(uid string, key []byte) {
	if len(key) != ed25519.PublicKeySize || userIDFor(key) != uid || d.store.Setting(personKeySetting(uid)) != "" {
		return
	}
	d.persist(d.store.SetSetting(personKeySetting(uid), hex.EncodeToString(key)))
}

// loadIdentity reads (or, the first time, creates) the user key and your
// profile.
//
// The key is made only when identity.key doesn't exist. One that can't be
// read, or isn't a key (the wrong length), is an error the daemon won't
// start without, and is left as it is: a new key is a new user id, which
// every paired phone, linked computer and shared Group would take for a
// stranger. To recover, fix the file (its permissions, a backup), or delete
// it to start again as someone new: phones then pair again, computers link
// again, and others let you into their Groups again.
func (d *Daemon) loadIdentity() error {
	path := filepath.Join(d.cfg.Dir, "identity.key")
	seed, err := readRetry(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		seed = make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return err
		}
		if err := writeFilesAtomic(privateFile{path, seed}); err != nil {
			return err
		}
	case err != nil:
		return fmt.Errorf("can't read this computer's identity, %s (%w): not making a new one, which would unpair your phones and linked computers. Fix the file, or delete it to start again with a new identity", path, err)
	case len(seed) != ed25519.SeedSize:
		return fmt.Errorf("this computer's identity, %s, is damaged (%d bytes, not %d): not making a new one, which would unpair your phones and linked computers. Restore the file, or delete it to start again with a new identity", path, len(seed), ed25519.SeedSize)
	}
	key := ed25519.NewKeyFromSeed(seed)
	name := d.store.Setting("profile.name")
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = personName(u)
			if name == "" {
				name = u.Username
			}
		}
	}
	avatar, _ := os.ReadFile(filepath.Join(d.cfg.Dir, "avatar.png"))
	d.id = identity{key: key, me: &pb.Person{
		UserId: userIDFor(key.Public().(ed25519.PublicKey)), Name: name,
		DeviceName: d.deviceName(), Avatar: avatar, Me: true,
	}}
	return nil
}

// personName is the full name of an account, from its GECOS field on Linux
// (its first part: "Sam Smith,Room 1,..."). root's is a description, not a
// person ("Console and webGui login account" on Unraid, "root" elsewhere).
func personName(u *user.User) string {
	if u.Uid == "0" {
		return ""
	}
	n, _, _ := strings.Cut(u.Name, ",")
	return strings.TrimSpace(n)
}

// deviceName is this Mac's name as others see it: the one set in Settings
// ▸ Sharing, else the Mac's own (System Settings ▸ General ▸ About ▸ Name,
// "Aloha Isle", not its network name "aloha-isle").
func (d *Daemon) deviceName() string {
	if n := strings.TrimSpace(d.store.Setting("profile.device")); n != "" {
		return n
	}
	return computerName()
}

// computerName is the Mac's own name, else its hostname.
var computerName = func() string {
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output(); err == nil {
			if n := strings.TrimSpace(string(out)); n != "" {
				return n
			}
		}
	}
	h, _ := hostname()
	return strings.TrimSuffix(h, ".local")
}

// setProfile changes your name and picture. Callers hold d.mu.
func (d *Daemon) setProfile(m *pb.SetProfile) error {
	name := strings.TrimSpace(m.Name)
	if name == "" {
		return errors.New("a name is needed")
	}
	if len(m.Avatar) > 256<<10 {
		return errors.New("picture too large")
	}
	if err := d.store.SetSetting("profile.name", name); err != nil {
		return err
	}
	if m.DeviceName != nil {
		if err := d.store.SetSetting("profile.device", strings.TrimSpace(*m.DeviceName)); err != nil {
			return err
		}
		d.id.me.DeviceName = d.deviceName()
		d.refreshLinked()
	}
	path := filepath.Join(d.cfg.Dir, "avatar.png")
	if len(m.Avatar) == 0 {
		_ = os.Remove(path)
	} else if err := os.WriteFile(path, m.Avatar, 0o600); err != nil {
		return err
	}
	d.id.me.Name, d.id.me.Avatar = name, m.Avatar
	d.publish(&pb.StateUpdate{People: []*pb.Person{proto.CloneOf(d.id.me)}})
	return nil
}

// readNoFollow reads a file the daemon owns, refusing a symlink in its place.
func readNoFollow(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

// readRetryWait is how long readRetry waits before trying again (doubling
// each time). A variable for tests.
var readRetryWait = 100 * time.Millisecond

// readRetry is readNoFollow, tried up to three times while it fails for any
// reason but the file not existing: a passing error (EIO, EMFILE) mustn't
// be taken for a damaged file.
func readRetry(path string) ([]byte, error) {
	var err error
	for i := range 3 {
		if i > 0 {
			time.Sleep(readRetryWait << (i - 1))
		}
		var b []byte
		if b, err = readNoFollow(path); err == nil || errors.Is(err, fs.ErrNotExist) {
			return b, err
		}
	}
	return nil, err
}

// privateFile is a file writeFilesAtomic writes.
type privateFile struct {
	path string
	data []byte
}

// syncFile flushes a file to disk. A variable so tests can make it fail.
var syncFile = (*os.File).Sync

// writeFilesAtomic writes new private files (0600) whole or not at all: each to
// a new temporary file beside it (O_EXCL, never through a symlink), synced,
// and only once all are written, renamed into place and their folders
// synced. A crash or error leaves no file, or a whole one, never a
// truncated one. On an error the temporary files are removed, and any
// already renamed into place too, so a set (a key pair) is all there or none.
func writeFilesAtomic(files ...privateFile) error {
	tmps := make([]string, 0, len(files))
	cleanup := func() {
		for _, t := range tmps {
			os.Remove(t)
		}
	}
	for _, pf := range files {
		var suffix [8]byte
		_, _ = rand.Read(suffix[:])
		tmp := filepath.Join(filepath.Dir(pf.path), "."+filepath.Base(pf.path)+".tmp-"+hex.EncodeToString(suffix[:]))
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			cleanup()
			return err
		}
		tmps = append(tmps, tmp)
		_, err = f.Write(pf.data)
		if err == nil {
			err = syncFile(f)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			cleanup()
			return err
		}
	}
	for i, pf := range files {
		if err := os.Rename(tmps[i], pf.path); err != nil {
			for _, done := range files[:i] {
				os.Remove(done.path) // new files: all of them or none
			}
			tmps = tmps[i:]
			cleanup()
			return err
		}
	}
	synced := map[string]bool{}
	for _, pf := range files {
		dir := filepath.Dir(pf.path)
		if synced[dir] {
			continue
		}
		synced[dir] = true
		if f, err := os.Open(dir); err == nil {
			err = syncFile(f)
			f.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}
