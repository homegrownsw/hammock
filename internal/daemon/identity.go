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

// identityError: identity.key exists but can't be read, or isn't a key.
type identityError struct {
	path   string
	reason string
}

func (e *identityError) Error() string {
	return fmt.Sprintf("this computer's identity (%s) %s, so remote access, phones, linked computers and shared Groups are off. Hammock won't make a new one, which would unpair them: fix or restore the file, or delete it and restart the Hammock Server to start again with a new identity", e.path, e.reason)
}

// loadUserKey reads the user key, making it the first time: only when
// identity.key doesn't exist. One that can't be read, or isn't a key (the
// wrong length), is an *identityError, and is left as it is: a new key is
// a new user id, which every paired phone, linked computer and shared
// Group would take for a stranger. To recover, fix the file (its
// permissions, a backup), or delete it to start again as someone new:
// phones then pair again, computers link again, and others let you into
// their Groups again.
func loadUserKey(path string) (ed25519.PrivateKey, error) {
	seed, err := readRetry(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		seed = make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return nil, err
		}
		if err := writeFilesAtomic(privateFile{path, seed}); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, &identityError{path, fmt.Sprintf("can't be read (%v)", err)}
	case len(seed) != ed25519.SeedSize:
		return nil, &identityError{path, fmt.Sprintf("is damaged (%d bytes, not %d)", len(seed), ed25519.SeedSize)}
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// loadIdentity reads (or, the first time, creates) the user key and your
// profile.
//
// With a key that can't be read (loadUserKey) the daemon still runs, for
// what's on this computer alone (its panes, Groups, agents, schedules),
// and d.idErr says why the rest is off (identityGate): remote access, and
// with it pairing and phones; linked computers; sharing and joining Groups;
// Hammock Key and push. Its stand-in is a key made afresh each time it
// starts, in memory only, for the local code that compares user ids (the
// Mac app's "me", a schedule's computer): nothing is linked, shared or
// signed with it, and this computer's own record (refreshSelf) isn't
// saved, so nothing kept outlives it but a schedule's computer, which is
// set again each time the daemon starts.
func (d *Daemon) loadIdentity() error {
	key, err := loadUserKey(filepath.Join(d.cfg.Dir, "identity.key"))
	if ie := (*identityError)(nil); errors.As(err, &ie) {
		d.idErr = ie
		d.log.Error("identity unavailable", "err", ie)
		_, key, err = ed25519.GenerateKey(rand.Reader)
	}
	if err != nil {
		return err
	}
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

// identityGate refuses what needs this computer's identity while it can't
// be read (loadIdentity): pairing, remote access, sharing and joining
// Groups, linking computers, Hammock Key. Turning remote access off is
// still allowed. d.idErr is set before the daemon serves anyone, never
// after, so it's read without d.mu.
func (d *Daemon) identityGate(msg *pb.ClientMessage) error {
	if d.idErr == nil {
		return nil
	}
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_SetRemoteAccess:
		if !m.SetRemoteAccess.Enabled {
			return nil
		}
	case *pb.ClientMessage_CreatePairing, *pb.ClientMessage_ShowPairing, *pb.ClientMessage_RegisterPush,
		*pb.ClientMessage_CreateInvite, *pb.ClientMessage_RevokeInvite, *pb.ClientMessage_RespondJoin,
		*pb.ClientMessage_SetMember, *pb.ClientMessage_StopSharing, *pb.ClientMessage_JoinShared, *pb.ClientMessage_LeaveShared,
		*pb.ClientMessage_DiscoverComputers, *pb.ClientMessage_LinkComputer, *pb.ClientMessage_RespondLink,
		*pb.ClientMessage_UnlinkComputer, *pb.ClientMessage_SetComputerTint, *pb.ClientMessage_CreateLinkInvite,
		*pb.ClientMessage_IntroduceComputers, *pb.ClientMessage_HammockKeyCommand:
	default:
		return nil
	}
	return d.idErr
}

// remoteUnavailable is why remote access can't be on, for Settings ▸
// Devices: this computer's identity or certificate can't be used. "" if
// it can (as far as is known). Callers hold d.mu.
func (d *Daemon) remoteUnavailable() string {
	if d.idErr != nil {
		return d.idErr.Error()
	}
	return d.remote.certErr
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
