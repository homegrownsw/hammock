package daemon

// Obsidian's links in an editor pane's Markdown preview: [[Note]] and
// ![[diagram.svg]] name a file anywhere in the note's vault, not a path, so
// the page asks the daemon which file each one is (ResolveEditorLinks), as
// Obsidian would find it, and for an embedded note's text.
//
// The vault is the nearest folder above the note with .obsidian in it (else
// the note's own folder). Its files are listed once, by name, and kept a
// while (vaultFresh), or less when a link finds nothing (a note just made).

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/regfile"
)

const (
	vaultMaxFiles   = 50_000          // files listed in a vault, at most
	vaultWalkTime   = 2 * time.Second // time spent listing one, at most
	vaultFresh      = 30 * time.Second
	vaultMissFresh  = 3 * time.Second // listed again sooner when a link finds nothing
	vaultCacheMax   = 8               // vaults kept listed
	editorNoteMax   = 1 << 20         // an embedded note's text, at most
	editorLinksMax  = 500             // targets in one request
	editorNotesMax  = 50              // embedded notes' texts in one request
	vaultMarkerName = ".obsidian"
)

// noteExts are the files an embed shows the text of.
var noteExts = map[string]bool{".md": true, ".markdown": true, ".mdown": true, ".mkd": true}

// vaultIndex is a vault's files by name.
type vaultIndex struct {
	root   string
	built  time.Time
	byName map[string][]string // lowercased base name → paths relative to root, "/"-separated
}

type vaultCache struct {
	mu  sync.Mutex
	idx map[string]*vaultIndex
}

var vaults = &vaultCache{idx: map[string]*vaultIndex{}}

// get is root's index, listed again when older than maxAge.
func (c *vaultCache) get(root string, maxAge time.Duration) *vaultIndex {
	c.mu.Lock()
	idx := c.idx[root]
	c.mu.Unlock()
	if idx != nil && time.Since(idx.built) < maxAge {
		return idx
	}
	idx = listVault(root)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.idx) >= vaultCacheMax && c.idx[root] == nil {
		oldest := ""
		for r, i := range c.idx {
			if oldest == "" || i.built.Before(c.idx[oldest].built) {
				oldest = r
			}
		}
		delete(c.idx, oldest)
	}
	c.idx[root] = idx
	return idx
}

// skipVaultDir reports whether a folder in a vault is left out, as Obsidian
// leaves it: its settings, its trash, git's, packages, anything hidden.
func skipVaultDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// listVault lists root's files by name, up to vaultMaxFiles or vaultWalkTime.
func listVault(root string) *vaultIndex {
	idx := &vaultIndex{root: root, built: time.Now(), byName: map[string][]string{}}
	deadline := time.Now().Add(vaultWalkTime)
	n := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skipVaultDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		n++
		if n > vaultMaxFiles || (n%256 == 0 && time.Now().After(deadline)) {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		key := strings.ToLower(name)
		idx.byName[key] = append(idx.byName[key], filepath.ToSlash(rel))
		return nil
	})
	return idx
}

// findVault is the vault dir is in: the nearest folder (dir or above) with
// .obsidian in it, else dir itself.
func findVault(dir string) string {
	for d := dir; ; {
		if st, err := os.Stat(filepath.Join(d, vaultMarkerName)); err == nil && st.IsDir() {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return dir
		}
		d = up
	}
}

// relInside reports whether rel (relative to a root) stays inside it.
func relInside(rel string) bool {
	return rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

// resolveVaultLink is the file a link's target names, from the note in
// fromDir, as Obsidian finds it: a path (folder/Note) from the vault's root,
// then from the note's folder, then any file whose path ends with it; a bare
// name anywhere in the vault. Without an extension, a note (.md) first. Among
// several, the one nearest the note. allow says whether a file may be given
// (a guest's limits); "" when nothing (allowed) matches. Results stay inside
// the vault.
func resolveVaultLink(idx *vaultIndex, fromDir, target string, allow func(string) bool) string {
	root := idx.root
	t := strings.TrimSpace(strings.ReplaceAll(target, "\\", "/"))
	t = strings.TrimPrefix(t, "/")
	if t == "" || strings.ContainsRune(t, 0) {
		return ""
	}
	names := []string{t + ".md", t}
	if filepath.Ext(t) != "" {
		names = []string{t, t + ".md"}
	}
	fromRel, err := filepath.Rel(root, fromDir)
	if err != nil || !relInside(fromRel) {
		fromRel = "."
	}
	fromRel = filepath.ToSlash(fromRel)
	// ok is abs if it's a file that may be given, in the case the vault has
	// it in (a Mac's disk finds "garden plan.md" as "Garden Plan.md"), else "".
	ok := func(abs string) string {
		rel, err := filepath.Rel(root, abs)
		if err != nil || !relInside(rel) {
			return ""
		}
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if skipVaultDir(part) {
				return "" // what listing it leaves out: .trash, hidden files
			}
		}
		st, err := os.Stat(abs)
		if err != nil || !st.Mode().IsRegular() || !allow(abs) {
			return ""
		}
		rel = filepath.ToSlash(rel)
		for _, r := range idx.byName[strings.ToLower(filepath.Base(abs))] {
			if r != rel && strings.EqualFold(r, rel) {
				return filepath.Join(root, filepath.FromSlash(r))
			}
		}
		return abs
	}
	for _, name := range names {
		if strings.Contains(name, "/") {
			for _, base := range []string{root, fromDir} {
				if abs := ok(filepath.Join(base, filepath.FromSlash(name))); abs != "" {
					return abs
				}
			}
		} else if abs := ok(filepath.Join(fromDir, name)); abs != "" {
			return abs // beside the note: nearest of all, even before it's listed
		}
		lower := strings.ToLower(name)
		base := lower[strings.LastIndex(lower, "/")+1:]
		var found []string
		for _, rel := range idx.byName[base] {
			l := strings.ToLower(rel)
			if l == lower || strings.HasSuffix(l, "/"+lower) {
				found = append(found, rel)
			}
		}
		sort.SliceStable(found, func(i, j int) bool {
			di, dj := linkDistance(fromRel, found[i]), linkDistance(fromRel, found[j])
			if di != dj {
				return di < dj
			}
			ei, ej := strings.HasSuffix(found[i], name), strings.HasSuffix(found[j], name) // the case as written
			if ei != ej {
				return ei
			}
			return found[i] < found[j]
		})
		for _, rel := range found {
			if abs := ok(filepath.Join(root, filepath.FromSlash(rel))); abs != "" {
				return abs
			}
		}
	}
	return ""
}

// linkDistance is how far file (relative to the vault) is from the folder
// fromDir (also relative): folders up to where they meet, then down.
func linkDistance(fromDir, file string) int {
	split := func(s string) []string {
		if s == "." || s == "" {
			return nil
		}
		return strings.Split(s, "/")
	}
	a := split(fromDir)
	b := split(filepath.ToSlash(filepath.Dir(filepath.FromSlash(file))))
	common := 0
	for common < len(a) && common < len(b) && a[common] == b[common] {
		common++
	}
	return len(a) - common + len(b) - common
}

// resolveEditorLinks answers ResolveEditorLinks. guest: a guest's, only
// files beside the pane's file (its folder and those in it) and in the
// Group's folder, as readEditorImage; else anything on this computer.
func (d *Daemon) resolveEditorLinks(m *pb.ResolveEditorLinks, folder string, guest bool) (*pb.ServerMessage, error) {
	h := d.editors
	h.mu.Lock()
	b, err := h.buf(m.PaneId)
	file := ""
	if err == nil {
		file = b.path
	}
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if len(m.Targets) > editorLinksMax {
		return nil, errors.New("too many links at once")
	}
	allow := func(string) bool { return true }
	if guest {
		docDir := filepath.Dir(file)
		allow = func(p string) bool {
			r, err := filepath.EvalSymlinks(p)
			return err == nil && insideFolder(r, folder) && insideFolder(r, docDir) && file != ""
		}
	}
	out := &pb.EditorLinks{From: m.From}
	from := file
	if m.From != "" {
		if !filepath.IsAbs(m.From) {
			return nil, errors.New("give an absolute path")
		}
		from = filepath.Clean(m.From)
	}
	if from == "" || !allow(from) {
		// Untitled, or a note a guest can't see: nothing found.
		for _, t := range m.Targets {
			out.Links = append(out.Links, &pb.EditorLink{Target: t})
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_EditorLinks{EditorLinks: out}}, nil
	}
	fromDir := filepath.Dir(from)
	if r, err := filepath.EvalSymlinks(fromDir); err == nil {
		fromDir = r
	}
	root := findVault(fromDir)
	idx := vaults.get(root, vaultFresh)
	paths := make([]string, len(m.Targets))
	missed := false
	for i, t := range m.Targets {
		paths[i] = resolveVaultLink(idx, fromDir, t, allow)
		missed = missed || paths[i] == ""
	}
	if missed && time.Since(idx.built) > vaultMissFresh {
		idx = vaults.get(root, vaultMissFresh)
		for i, t := range m.Targets {
			if paths[i] == "" {
				paths[i] = resolveVaultLink(idx, fromDir, t, allow)
			}
		}
	}
	wantText := map[string]bool{}
	for _, n := range m.Notes {
		if len(wantText) >= editorNotesMax {
			break
		}
		wantText[n] = true
	}
	for i, t := range m.Targets {
		l := &pb.EditorLink{Target: t, Path: paths[i]}
		if paths[i] != "" && wantText[t] && noteExts[strings.ToLower(filepath.Ext(paths[i]))] {
			if text, ok := d.noteText(paths[i]); ok {
				l.Text, l.HasText = text, true
			}
		}
		out.Links = append(out.Links, l)
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_EditorLinks{EditorLinks: out}}, nil
}

// noteText is an embedded note's text: its editor buffer's, when it's open
// (edits not yet saved show), else the file's, if it's UTF-8 text of
// editorNoteMax or less.
func (d *Daemon) noteText(path string) (string, bool) {
	real, err := resolveEditorPath(path, "")
	if err != nil {
		return "", false
	}
	h := d.editors
	h.mu.Lock()
	if b := h.bufs[real]; b != nil && b.path == real {
		text := b.text.String()
		h.mu.Unlock()
		if len(text) > editorNoteMax {
			return "", false
		}
		return text, true
	}
	h.mu.Unlock()
	data, err := regfile.ReadFile(real, editorNoteMax)
	if err != nil || !utf8.Valid(data) {
		return "", false
	}
	return string(data), true
}
