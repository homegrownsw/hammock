package daemon

// Groups' pictures: a Group whose folder has .hammock/icon.png (or .jpg)
// shows it, unless it has an emoji. The daemon finds it, checks it (a cloned
// repo's file is untrusted: no symlinks, a size and pixel cap, decoded by Go's
// own decoders), shrinks it to 128 px and serves that; the Group carries its
// hash, so clients fetch it once per change. Setting one writes the file.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // decoders for image.Decode
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

const (
	iconServed  = 128      // the side of the picture clients get
	iconWritten = 256      // and of the one written to a folder
	iconMaxFile = 10 << 20 // bytes read from a folder
	iconMaxSide = 8192     // pixels, before decoding
)

// iconNames are the files looked for in a folder's .hammock.
var iconNames = []string{"icon.png", "icon.jpg", "icon.jpeg"}

type groupIcons struct {
	mu sync.Mutex
	by map[string]*groupIcon // group id → its picture
}

type groupIcon struct {
	path string
	mod  time.Time
	size int64
	hash string
	png  []byte
}

// iconFile finds the picture in folder's .hammock: a regular file (not a link,
// nor in a linked .hammock), else "".
func iconFile(folder string) (string, os.FileInfo) {
	if folder == "" {
		return "", nil
	}
	dir := filepath.Join(folder, ".hammock")
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return "", nil
	}
	for _, n := range iconNames {
		p := filepath.Join(dir, n)
		if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() && st.Size() <= iconMaxFile {
			return p, st
		}
	}
	return "", nil
}

// decodeIcon checks and decodes image data (PNG or JPEG).
func decodeIcon(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("not a PNG or JPEG image")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > iconMaxSide || cfg.Height > iconMaxSide {
		return nil, fmt.Errorf("the image is %d×%d: at most %d a side", cfg.Width, cfg.Height, iconMaxSide)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("couldn't read the image")
	}
	return img, nil
}

// shrink scales img to fit side×side (never up), averaging the pixels each
// output pixel covers, centred on a transparent square.
func shrink(img image.Image, side int) *image.NRGBA {
	// Without the transparent margin (app icons have one built in), so
	// every picture fills its slot.
	src := image.NewNRGBA(img.Bounds())
	draw.Draw(src, src.Bounds(), img, img.Bounds().Min, draw.Src)
	b := opaqueBounds(src)
	w, h := b.Dx(), b.Dy()
	scale := float64(side) / float64(max(w, h))
	if scale > 1 {
		scale = 1
	}
	ow, oh := max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
	out := image.NewNRGBA(image.Rect(0, 0, max(ow, oh), max(ow, oh)))
	offX, offY := (out.Bounds().Dx()-ow)/2, (out.Bounds().Dy()-oh)/2
	for y := 0; y < oh; y++ {
		y0, y1 := y*h/oh, max((y+1)*h/oh, y*h/oh+1)
		for x := 0; x < ow; x++ {
			x0, x1 := x*w/ow, max((x+1)*w/ow, x*w/ow+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := src.NRGBAAt(b.Min.X+sx, b.Min.Y+sy)
					// Weighted by alpha, so transparent pixels don't darken edges.
					r += uint64(c.R) * uint64(c.A)
					g += uint64(c.G) * uint64(c.A)
					bl += uint64(c.B) * uint64(c.A)
					a += uint64(c.A)
					n++
				}
			}
			if a > 0 {
				out.SetNRGBA(offX+x, offY+y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(bl / a), uint8(a / n)})
			}
		}
	}
	return out
}

// opaqueBounds is the smallest rectangle holding img's visible pixels
// (all of it if there are none).
func opaqueBounds(img *image.NRGBA) image.Rectangle {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A > 160 { // not the soft shadow round an app icon
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			}
		}
	}
	if maxX < minX {
		return b
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// loadIcon reads, checks and shrinks the picture at path.
func loadIcon(path string) (*groupIcon, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, err := decodeIcon(data)
	if err != nil {
		return nil, err
	}
	out := encodePNG(shrink(img, iconServed))
	sum := sha256.Sum256(out)
	return &groupIcon{path: path, png: out, hash: hex.EncodeToString(sum[:8])}, nil
}

// watchGroupIcons looks for changed pictures every few seconds (a stat per
// Group) until the daemon stops.
func (d *Daemon) watchGroupIcons() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		d.scanGroupIcons()
		select {
		case <-d.done:
			return
		case <-t.C:
		}
	}
}

// scanGroupIcons updates each Group's picture from its folder, telling
// clients of the ones that changed.
func (d *Daemon) scanGroupIcons() {
	type job struct {
		id      string
		folders []string
	}
	d.mu.Lock()
	jobs := make([]job, 0, len(d.groups))
	for id, g := range d.groups {
		jobs = append(jobs, job{id, d.iconFolders(g)})
	}
	d.mu.Unlock()

	d.icons.mu.Lock()
	if d.icons.by == nil {
		d.icons.by = map[string]*groupIcon{}
	}
	changed := map[string]string{} // group → its new hash
	for _, j := range jobs {
		old := d.icons.by[j.id]
		var path string
		var st os.FileInfo
		// One set in Hammock first, then one in its folders.
		if p := d.storedPicture(j.id); fileIsRegular(p) {
			path = p
			st, _ = os.Lstat(p)
		}
		for _, f := range j.folders {
			if path != "" {
				break
			}
			if path, st = iconFile(f); path != "" {
				break
			}
		}
		switch {
		case path == "" && old == nil:
		case path == "":
			delete(d.icons.by, j.id)
			changed[j.id] = ""
		case old != nil && old.path == path && old.mod.Equal(st.ModTime()) && old.size == st.Size():
		default:
			ic, err := loadIcon(path)
			if err != nil {
				d.log.Info("group picture unreadable", "path", path, "err", err)
				ic = &groupIcon{path: path}
			}
			ic.mod, ic.size = st.ModTime(), st.Size()
			d.icons.by[j.id] = ic
			if old == nil || old.hash != ic.hash {
				changed[j.id] = ic.hash
			}
		}
	}
	listed := map[string]bool{}
	for _, j := range jobs {
		listed[j.id] = true
	}
	for id := range d.icons.by {
		if !listed[id] {
			delete(d.icons.by, id)
		}
	}
	d.icons.mu.Unlock()
	if len(changed) == 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	u := &pb.StateUpdate{}
	for id, hash := range changed {
		g := d.groups[id]
		if g == nil || g.IconImage == hash {
			continue
		}
		g.IconImage = hash
		d.persist(d.store.PutGroup(g))
		u.Groups = append(u.Groups, proto.CloneOf(g))
	}
	if len(u.Groups) > 0 {
		d.publish(u)
	}
}

// iconFolders are where a Group's picture may be: its terminals' folders
// (the last focused first) and the folders above them, short of the home
// folder; so a picture at a repo's root shows whichever folder in it you
// last worked in. Callers hold d.mu.
func (d *Daemon) iconFolders(g *pb.Group) []string {
	home, _ := os.UserHomeDir()
	var out []string
	seen := map[string]bool{}
	add := func(dir string) {
		for dir != "" && dir != "/" && dir != home && dir != "." && !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
			dir = filepath.Dir(dir)
		}
	}
	add(d.groupFolder(g))
	for _, tid := range g.TabIds {
		if t := d.tabs[tid]; t != nil {
			for _, id := range tabPanes(t) {
				if p := d.panes[id]; p != nil && p.isTerminal() {
					add(p.cwd())
				}
			}
		}
	}
	if len(out) == 0 {
		if f := d.groupFolder(g); f != "" {
			out = append(out, f) // the home folder itself, if that's where it works
		}
	}
	return out
}

// readGroupIcon answers ReadGroupIcon.
func (d *Daemon) readGroupIcon(m *pb.ReadGroupIcon) (*pb.GroupIcon, error) {
	d.icons.mu.Lock()
	defer d.icons.mu.Unlock()
	ic := d.icons.by[m.GroupId]
	if ic == nil || len(ic.png) == 0 {
		return nil, errors.New("this Group has no picture")
	}
	return &pb.GroupIcon{GroupId: m.GroupId, Hash: ic.hash, Png: ic.png}, nil
}

// storedPicture is where a picture set in Hammock is kept: with the daemon's
// own state, not in anyone's project.
func (d *Daemon) storedPicture(groupID string) string {
	return filepath.Join(d.cfg.Dir, "grouppics", groupID+".png")
}

// setGroupIconImage keeps (or with no image, forgets) the picture set for a
// Group in Hammock, then looks again. A .hammock/icon.png in its folder isn't
// touched: it shows when none is set here.
func (d *Daemon) setGroupIconImage(m *pb.SetGroupIconImage) error {
	d.mu.Lock()
	_, ok := d.groups[m.GroupId]
	d.mu.Unlock()
	if !ok {
		return errors.New("no such group")
	}
	path := d.storedPicture(m.GroupId)
	if len(m.Image) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		d.scanGroupIcons()
		return nil
	}
	if len(m.Image) > iconMaxFile {
		return errors.New("the image is over 10 MB")
	}
	img, err := decodeIcon(m.Image)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".icon-*.png")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(encodePNG(shrink(img, iconWritten)))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	d.scanGroupIcons()
	return nil
}

func fileIsRegular(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode().IsRegular()
}
