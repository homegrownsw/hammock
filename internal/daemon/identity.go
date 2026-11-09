package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"google.golang.org/protobuf/proto"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
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

// loadIdentity reads (or creates) the user key and your profile.
func (d *Daemon) loadIdentity() error {
	path := filepath.Join(d.cfg.Dir, "identity.key")
	seed, err := readNoFollow(path)
	if err != nil || len(seed) != ed25519.SeedSize {
		seed = make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return err
		}
		if err := writeNoFollow(path, seed); err != nil {
			return err
		}
	}
	key := ed25519.NewKeyFromSeed(seed)
	name := d.store.Setting("profile.name")
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = strings.TrimSpace(u.Name)
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

// writeNoFollow writes a private file, refusing a symlink in its place.
func writeNoFollow(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
