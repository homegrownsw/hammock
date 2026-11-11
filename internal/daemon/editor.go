package daemon

// Editor panes: a text file in a pane, its buffer kept here so every pane
// and client showing the file (this Mac, a phone, the TUI, a guest) edits
// the same text, and unsaved edits outlive the apps and the daemon. Edits
// follow CodeMirror's collab model (see EditorPush in the proto): the buffer
// takes a client's updates only at its current version, and every update
// goes to every attached client. The file is watched: one changed on disk
// reloads a buffer with nothing unsaved, else the panes say so (conflict).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/homegrownsw/hammock/internal/gitdiff"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/textbuf"
	"google.golang.org/protobuf/proto"
)

const (
	editorLargeFile = 5 << 20 // bigger: opened only when asked (EditorOpen.large)
	editorRecord    = "editorbuf"
)

// errLargeFile starts the error for a file over editorLargeFile, so clients
// can offer to open it anyway.
const errLargeFile = "large file: "

type editorSel struct {
	version      uint64
	anchor, head uint32
}

// editorBuf is one file's buffer.
type editorBuf struct {
	key     string // its path, or "untitled:<pane id>"
	path    string // "" while untitled
	text    textbuf.Text
	version uint64

	disk     textbuf.Text // the file as last loaded or saved (lines in "\n")
	diskSum  [32]byte     // of the file's bytes as last seen
	diskMod  time.Time
	diskSize int64
	eol      lineEnds // how disk's lines end in the file
	mode     fs.FileMode

	dirty, conflict, missing, readOnly bool
	language                           string
	tabWidth                           uint32
	indent                             string

	panes    map[string]bool                // pane ids showing it
	watchers map[*conn]map[string]bool      // attached clients → the panes they attached
	sels     map[string]editorSel           // pane → its selection, as last reported
	reveal   map[string]*pb.EditorSelection // pane → a selection to send when it's next attached

	head      []string // the file at git's HEAD (nil: not in git)
	headKnown bool
	marks     []*pb.GitMark
	marksDue  bool
	saveDue   bool // a dirty buffer not yet kept in the store
}

type editorHub struct {
	d    *Daemon
	mu   sync.Mutex
	bufs map[string]*editorBuf // by key
	pane map[string]string     // pane id → buffer key
	once sync.Once
	stop chan struct{}

	lmu   sync.Mutex
	lists map[string]*fileListing // folder → its files, briefly cached
}

func newEditorHub(d *Daemon) *editorHub {
	return &editorHub{d: d, bufs: map[string]*editorBuf{}, pane: map[string]string{}, stop: make(chan struct{}), lists: map[string]*fileListing{}}
}

func (h *editorHub) start() {
	h.once.Do(func() { go h.run() })
}

// run watches the files and keeps git marks and unsaved buffers current.
func (h *editorHub) run() {
	tick := time.NewTicker(700 * time.Millisecond)
	defer tick.Stop()
	n := 0
	for {
		select {
		case <-h.stop:
			return
		case <-tick.C:
		}
		n++
		h.mu.Lock()
		bufs := make([]*editorBuf, 0, len(h.bufs))
		for _, b := range h.bufs {
			bufs = append(bufs, b)
		}
		h.mu.Unlock()
		for _, b := range bufs {
			h.checkDisk(b)
			h.refreshMarks(b, false)
		}
		h.keepUnsaved(false)
	}
}

func (h *editorHub) close() {
	select {
	case <-h.stop:
	default:
		close(h.stop)
	}
	h.keepUnsaved(true)
}

// ---- opening ----

// readTextFile reads path for the editor, refusing what isn't text.
func readTextFile(path string, large bool) (data []byte, st fs.FileInfo, err error) {
	st, err = os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if st.IsDir() {
		return nil, nil, fmt.Errorf("%s is a folder", filepath.Base(path))
	}
	if !st.Mode().IsRegular() {
		// A pipe, a device or a socket: reading one could block (or never
		// end), and the daemon reads while holding its lock.
		return nil, nil, fmt.Errorf("%s isn't a regular file", filepath.Base(path))
	}
	if st.Size() > editorLargeFile && !large {
		return nil, nil, fmt.Errorf("%s%s is %s: open it anyway?", errLargeFile, filepath.Base(path), humanSize(st.Size()))
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	head := data[:min(len(data), 8000)]
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, nil, fmt.Errorf("%s isn't a text file", filepath.Base(path))
	}
	if !utf8.Valid(data) {
		return nil, nil, fmt.Errorf("%s isn't UTF-8 text", filepath.Base(path))
	}
	return data, st, nil
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// resolveEditorPath makes path absolute (relative to base) and clean,
// following symlinks where it exists, so one file is one buffer.
func resolveEditorPath(path, base string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		if base == "" {
			return "", errors.New("a relative path needs a folder")
		}
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r, nil
	}
	// Not there yet: resolve its folder.
	if dir, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(dir, filepath.Base(path)), nil
	}
	return path, nil
}

// insideFolder reports whether path is folder or inside it.
func insideFolder(path, folder string) bool {
	if folder == "" {
		return false
	}
	if r, err := filepath.EvalSymlinks(folder); err == nil {
		folder = r
	}
	rel, err := filepath.Rel(folder, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

// load opens path's buffer (or finds it open), for a new pane.
func (h *editorHub) load(path string, large bool) (*editorBuf, error) {
	h.mu.Lock()
	if b := h.bufs[path]; b != nil {
		h.mu.Unlock()
		return b, nil
	}
	h.mu.Unlock()
	b := &editorBuf{key: path, path: path, panes: map[string]bool{}, watchers: map[*conn]map[string]bool{}, sels: map[string]editorSel{}, reveal: map[string]*pb.EditorSelection{}, mode: 0o644}
	data, st, err := readTextFile(path, large)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		b.missing = true // new: created when saved
	case err != nil:
		return nil, err
	default:
		b.text, b.eol = normaliseText(data)
		b.disk = b.text
		b.diskSum, b.diskMod, b.diskSize, b.mode = sha256.Sum256(data), st.ModTime(), st.Size(), st.Mode().Perm()
		b.readOnly = !writable(path)
	}
	b.tabWidth, b.indent = indentFor(path, b.text)
	h.mu.Lock()
	defer h.mu.Unlock()
	if o := h.bufs[path]; o != nil {
		return o, nil // opened meanwhile
	}
	h.bufs[path] = b
	h.start()
	// git can be slow in a big repository, and callers may hold d.mu: the
	// marks follow when it answers.
	go func() {
		h.loadHead(b)
		h.refreshMarks(b, true)
	}()
	return b, nil
}

func writable(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func newUntitled(paneID string) *editorBuf {
	return &editorBuf{key: "untitled:" + paneID, panes: map[string]bool{}, watchers: map[*conn]map[string]bool{}, sels: map[string]editorSel{}, reveal: map[string]*pb.EditorSelection{}, mode: 0o644}
}

func (b *editorBuf) name() string {
	if b.path == "" {
		return "Untitled"
	}
	return filepath.Base(b.path)
}

// info is the pane's EditorInfo. Callers hold h.mu.
func (b *editorBuf) info(folder string) *pb.EditorInfo {
	return &pb.EditorInfo{
		Path: b.path, Name: b.name(), Dirty: b.dirty, Conflict: b.conflict, Missing: b.missing && b.path != "",
		ReadOnly: b.readOnly, Language: b.language, Folder: folder, TabWidth: b.tabWidth, Indent: b.indent,
	}
}

// startEditor creates an editor pane for tab t (not yet placed in its
// layout). Callers hold d.mu.
func (d *Daemon) startEditor(t *pb.Tab, spec *pb.PaneSpec) (*pane, error) {
	open := spec.Editor
	folder := ""
	if g := d.groups[t.GroupId]; g != nil {
		folder = d.groupFolder(g)
	}
	id := newID("pn")
	var b *editorBuf
	if open.Path != "" {
		base := open.Cwd
		if base == "" {
			base = folder
		}
		if base == "" {
			base = spec.Cwd
		}
		path, err := resolveEditorPath(open.Path, base)
		if err != nil {
			return nil, err
		}
		// Off d.mu would be kinder for a slow disk, but the pane must
		// exist when this returns; files are read quickly.
		if b, err = d.editors.load(path, open.Large); err != nil {
			return nil, err
		}
	} else {
		b = newUntitled(id)
	}
	h := d.editors
	h.mu.Lock()
	h.bufs[b.key] = b
	h.pane[id] = b.key
	b.panes[id] = true
	if open.Line > 0 {
		s := b.text.Offset(int(open.Line), int(open.Column))
		e := s
		if open.EndLine > 0 {
			e = b.text.Offset(int(open.EndLine), int(open.EndColumn))
			if open.EndColumn == 0 {
				e = b.text.Offset(int(open.EndLine), 1<<30) // the whole last line, as ACTION_SELECT
			}
		}
		b.reveal[id] = &pb.EditorSelection{Anchor: uint32(s), Head: uint32(e)}
		b.sels[id] = editorSel{version: b.version, anchor: uint32(s), head: uint32(e)}
	}
	info := &pb.Pane{
		Id: id, TabId: t.Id, Kind: pb.PaneKind_PANE_KIND_EDITOR, Title: b.name(),
		Cwd: editorCwd(b.path, folder, spec.Cwd), CreatedByPaneId: spec.CreatedByPaneId, Editor: b.info(folder),
	}
	h.mu.Unlock()
	h.start()
	p := newPane(d, info)
	d.panes[id] = p
	d.persist(d.store.PutPane(info))
	return p, nil
}

func editorCwd(path, folder, fallback string) string {
	if path != "" {
		return filepath.Dir(path)
	}
	if folder != "" {
		return folder
	}
	return fallback
}

// restore reopens an editor pane's buffer after the daemon restarts, with
// any unsaved edits it had. Callers hold d.mu.
func (h *editorHub) restore(p *pane, saved map[string][]byte) {
	info := p.info
	ed := info.Editor
	if ed == nil {
		ed = &pb.EditorInfo{}
		info.Editor = ed
	}
	var b *editorBuf
	key := ed.Path
	if key == "" {
		key = "untitled:" + info.Id
	}
	h.mu.Lock()
	b = h.bufs[key]
	h.mu.Unlock()
	if b == nil {
		if ed.Path != "" {
			var err error
			b, err = h.load(ed.Path, true)
			if err != nil {
				h.d.log.Warn("editor pane: couldn't reopen its file", "pane", info.Id, "path", ed.Path, "err", err)
				b = &editorBuf{key: key, path: ed.Path, panes: map[string]bool{}, watchers: map[*conn]map[string]bool{}, sels: map[string]editorSel{}, reveal: map[string]*pb.EditorSelection{}, missing: true, mode: 0o644}
			}
		} else {
			b = newUntitled(info.Id)
		}
		// load may have shared b already (its git lookup runs on): under h.mu.
		h.mu.Lock()
		b.language = ed.Language
		if raw, ok := saved[key]; ok {
			kept := &pb.EditorUpdate{}
			if proto.Unmarshal(raw, kept) == nil {
				base := textbuf.FromString(kept.DiskText)
				b.text = textbuf.FromString(kept.Text)
				b.dirty = true
				b.marksDue = true
				// The file changed while the daemon was away: as any change
				// under unsaved edits.
				if b.path != "" && (b.missing != kept.DiskMissing || (!b.missing && b.disk.String() != base.String())) {
					b.conflict = true
				}
			}
		}
		if o := h.bufs[key]; o != nil {
			b = o
		} else {
			h.bufs[key] = b
		}
		h.mu.Unlock()
	}
	h.mu.Lock()
	b.panes[info.Id] = true
	h.pane[info.Id] = key
	info.Editor = b.info(ed.Folder)
	info.Title = b.name()
	h.mu.Unlock()
	h.start()
}

// paneClosed lets go of a closed pane; its buffer goes with its last pane
// (unsaved edits too: clients ask first).
func (h *editorHub) paneClosed(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key, ok := h.pane[id]
	if !ok {
		return
	}
	delete(h.pane, id)
	b := h.bufs[key]
	if b == nil {
		return
	}
	delete(b.panes, id)
	delete(b.sels, id)
	delete(b.reveal, id)
	for c, ps := range b.watchers {
		delete(ps, id)
		if len(ps) == 0 {
			delete(b.watchers, c)
		}
	}
	if len(b.panes) == 0 {
		delete(h.bufs, key)
		if b.saveDue || b.dirty {
			h.d.persist(h.d.store.DeleteRecord(editorRecord, key))
		}
	}
}

// buf is pane id's buffer. Callers hold h.mu.
func (h *editorHub) buf(id string) (*editorBuf, error) {
	b := h.bufs[h.pane[id]]
	if b == nil {
		return nil, fmt.Errorf("pane %s isn't an editor pane", id)
	}
	return b, nil
}

// ---- clients ----

// attach sends c the buffer and keeps it up to date for pane id.
func (h *editorHub) attach(c *conn, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, err := h.buf(id)
	if err != nil {
		return
	}
	if b.watchers[c] == nil {
		b.watchers[c] = map[string]bool{}
	}
	b.watchers[c][id] = true
	u := &pb.EditorUpdate{PaneId: id, Version: b.version, Snapshot: true, Text: b.text.String(), Git: b.marks, GitKnown: b.headKnown}
	if s := b.reveal[id]; s != nil {
		u.Selection = s
		delete(b.reveal, id)
	} else if s, ok := b.sels[id]; ok && s.version == b.version {
		u.Selection = &pb.EditorSelection{Anchor: s.anchor, Head: s.head}
	}
	c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_EditorUpdate{EditorUpdate: u}})
}

func (h *editorHub) detach(c *conn, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key, ok := h.pane[id]
	if !ok {
		return
	}
	if b := h.bufs[key]; b != nil {
		delete(b.watchers[c], id)
		if len(b.watchers[c]) == 0 {
			delete(b.watchers, c)
		}
	}
}

// broadcast sends u (per pane) to everyone attached. Callers hold h.mu.
func (b *editorBuf) broadcast(u *pb.EditorUpdate) {
	for c, ps := range b.watchers {
		for id := range ps {
			x := proto.CloneOf(u)
			x.PaneId = id
			c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_EditorUpdate{EditorUpdate: x}})
		}
	}
}

// apply takes updates made at the buffer's version. Callers hold h.mu.
func (b *editorBuf) apply(updates []*pb.EditorChange) error {
	text := b.text
	var sets []textbuf.ChangeSet
	for _, u := range updates {
		cs, err := textbuf.ParseJSON(u.Changes)
		if err != nil {
			return err
		}
		if text, err = cs.Apply(text); err != nil {
			return err
		}
		sets = append(sets, cs)
	}
	// Selections follow the text, so an agent reading one after you type
	// still sees what you selected.
	for id, s := range b.sels {
		if s.version != b.version {
			continue
		}
		a, h := int(s.anchor), int(s.head)
		for _, cs := range sets {
			a, h = cs.MapPos(a, 1), cs.MapPos(h, 1)
		}
		b.sels[id] = editorSel{version: b.version + uint64(len(updates)), anchor: uint32(a), head: uint32(h)}
	}
	b.text = text
	b.version += uint64(len(updates))
	b.broadcast(&pb.EditorUpdate{Version: b.version, Updates: updates})
	return nil
}

// changed notes the text changed; reports whether dirty did. Callers hold h.mu.
func (b *editorBuf) changed() bool {
	was := b.dirty
	b.dirty = b.path == "" && len(b.text) > 0 || b.path != "" && (b.missing || !slices.Equal(b.text, b.disk))
	b.marksDue = true
	b.saveDue = true
	return was != b.dirty
}

// push answers EditorPush.
func (h *editorHub) push(m *pb.EditorPush) (bool, error) {
	h.mu.Lock()
	b, err := h.buf(m.PaneId)
	if err != nil {
		h.mu.Unlock()
		return false, err
	}
	if b.readOnly && len(m.Updates) > 0 {
		h.mu.Unlock()
		return false, errReadOnly
	}
	if m.Version != b.version {
		h.mu.Unlock()
		return false, nil // it gets what it hasn't seen, rebases and pushes again
	}
	if len(m.Updates) == 0 {
		h.mu.Unlock()
		return true, nil
	}
	if err := b.apply(m.Updates); err != nil {
		h.mu.Unlock()
		return false, err
	}
	flip := b.changed()
	h.mu.Unlock()
	if flip {
		h.publish(b)
	}
	return true, nil
}

// replaceText makes the buffer's text t, as an update from "disk". Callers
// hold h.mu.
func (b *editorBuf) replaceText(t textbuf.Text) {
	cs := textbuf.Diff(b.text, t)
	if cs.Empty() {
		return
	}
	_ = b.apply([]*pb.EditorChange{{Changes: cs.JSON(), ClientId: "disk"}})
}

// selectPane answers EditorSelect.
func (h *editorHub) selectPane(m *pb.EditorSelect) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b, err := h.buf(m.PaneId); err == nil {
		b.sels[m.PaneId] = editorSel{version: m.Version, anchor: m.Anchor, head: m.Head}
	}
}

// publish tells everyone the panes of b changed (dirty, conflict, a name).
// Callers don't hold h.mu or d.mu.
func (h *editorHub) publish(b *editorBuf) {
	d := h.d
	h.mu.Lock()
	ids := make([]string, 0, len(b.panes))
	for id := range b.panes {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, id := range ids {
		p := d.panes[id]
		if p == nil {
			continue
		}
		folder := ""
		if g := d.groups[d.groupOfPane(id)]; g != nil {
			folder = d.groupFolder(g)
		}
		h.mu.Lock()
		ed := b.info(folder)
		name, path := b.name(), b.path
		h.mu.Unlock()
		p.mu.Lock()
		changed := !proto.Equal(p.info.Editor, ed) || p.info.Title != name
		p.info.Editor, p.info.Title = ed, name
		if path != "" {
			p.info.Cwd = filepath.Dir(path)
		}
		p.mu.Unlock()
		if changed {
			d.paneChangedIfLive(p)
		}
	}
}

// command answers EditorCommand. guestFolder: a guest's, saving only in
// the Group's folder.
func (h *editorHub) command(m *pb.EditorCommand, folder string, guest bool) error {
	switch m.Action {
	case pb.EditorCommand_ACTION_SAVE:
		return h.save(m, folder, guest)
	case pb.EditorCommand_ACTION_RELOAD:
		h.mu.Lock()
		b, err := h.buf(m.PaneId)
		if err != nil {
			h.mu.Unlock()
			return err
		}
		path := b.path
		if path == "" {
			h.mu.Unlock()
			return errors.New("it hasn't been saved yet")
		}
		h.mu.Unlock()
		data, st, err := readTextFile(path, true)
		h.mu.Lock()
		if errors.Is(err, fs.ErrNotExist) {
			b.missing = true
			b.disk = nil
			b.replaceText(nil)
		} else if err != nil {
			h.mu.Unlock()
			return err
		} else {
			t, eol := normaliseText(data)
			b.disk, b.eol, b.missing = t, eol, false
			b.diskSum, b.diskMod, b.diskSize = sha256.Sum256(data), st.ModTime(), st.Size()
			b.mode, b.readOnly = st.Mode().Perm(), !writable(path)
			b.replaceText(t)
		}
		b.conflict = false
		b.changed()
		h.mu.Unlock()
		h.publish(b)
		return nil
	case pb.EditorCommand_ACTION_KEEP_MINE:
		h.mu.Lock()
		b, err := h.buf(m.PaneId)
		if err != nil {
			h.mu.Unlock()
			return err
		}
		b.conflict = false
		h.mu.Unlock()
		h.publish(b)
		return nil
	case pb.EditorCommand_ACTION_SET_LANGUAGE:
		h.mu.Lock()
		b, err := h.buf(m.PaneId)
		if err != nil {
			h.mu.Unlock()
			return err
		}
		b.language = m.Language
		h.mu.Unlock()
		h.publish(b)
		return nil
	case pb.EditorCommand_ACTION_SELECT:
		h.mu.Lock()
		defer h.mu.Unlock()
		b, err := h.buf(m.PaneId)
		if err != nil {
			return err
		}
		line := max(int(m.Line), 1)
		s := b.text.Offset(line, int(m.Column))
		e := s
		if m.EndLine > 0 {
			e = b.text.Offset(int(m.EndLine), int(m.EndColumn))
			if m.EndColumn == 0 {
				// A whole last line.
				l, _ := b.text.LineCol(e)
				e = b.text.Offset(l, 1<<30)
			}
		}
		sel := &pb.EditorSelection{Anchor: uint32(s), Head: uint32(e)}
		b.sels[m.PaneId] = editorSel{version: b.version, anchor: sel.Anchor, head: sel.Head}
		sent := false
		for c, ps := range b.watchers {
			if ps[m.PaneId] {
				c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_EditorUpdate{EditorUpdate: &pb.EditorUpdate{PaneId: m.PaneId, Version: b.version, Selection: sel}}})
				sent = true
			}
		}
		if !sent {
			b.reveal[m.PaneId] = sel
		}
		return nil
	}
	return fmt.Errorf("unknown editor action %v", m.Action)
}

// save writes the buffer to its file (or path: Save As).
func (h *editorHub) save(m *pb.EditorCommand, folder string, guest bool) error {
	h.mu.Lock()
	b, err := h.buf(m.PaneId)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	target := b.path
	if m.Path != "" {
		base := folder
		if base == "" && b.path != "" {
			base = filepath.Dir(b.path)
		}
		h.mu.Unlock()
		target, err = resolveEditorPath(m.Path, base)
		if err != nil {
			return err
		}
		h.mu.Lock()
	}
	if target == "" {
		h.mu.Unlock()
		return errors.New("save it as what? (a path)")
	}
	if guest && !insideFolder(target, folder) {
		h.mu.Unlock()
		return errors.New("guests save only inside the Group's folder")
	}
	renaming := target != b.path
	if !renaming && b.readOnly {
		h.mu.Unlock()
		return errReadOnly
	}
	if o := h.bufs[target]; renaming && o != nil && o != b {
		h.mu.Unlock()
		return fmt.Errorf("%s is open in another pane", filepath.Base(target))
	}
	if !renaming && b.conflict && !m.Force {
		h.mu.Unlock()
		return errors.New("the file changed on disk since: reload it, or save anyway")
	}
	if renaming && !m.Force {
		if _, err := os.Stat(target); err == nil {
			h.mu.Unlock()
			return fmt.Errorf("%s already exists: replace it?", filepath.Base(target))
		}
	}
	text, disk, eol, mode := b.text, b.disk, b.eol, b.mode
	h.mu.Unlock()

	out, wrote := eol.render(text, disk)
	if st, err := os.Stat(target); err == nil {
		mode = st.Mode().Perm()
	}
	if err := writeFileAtomic(target, out, mode); err != nil {
		return err
	}
	st, _ := os.Stat(target)

	h.mu.Lock()
	if renaming {
		oldKey := b.key
		delete(h.bufs, oldKey)
		if b.saveDue || b.dirty {
			h.d.persist(h.d.store.DeleteRecord(editorRecord, oldKey))
		}
		b.key, b.path = target, target
		h.bufs[target] = b
		for id := range b.panes {
			h.pane[id] = target
		}
		b.tabWidth, b.indent = indentFor(target, text)
	}
	b.disk, b.eol = text, wrote
	b.diskSum = sha256.Sum256(out)
	if st != nil {
		b.diskMod, b.diskSize = st.ModTime(), st.Size()
	}
	b.missing, b.conflict, b.readOnly = false, false, false
	b.changed() // dirty only if edited since text was taken
	h.mu.Unlock()
	if renaming {
		h.loadHead(b)
		h.refreshMarks(b, true)
	} else {
		h.mu.Lock()
		b.marksDue = true
		h.mu.Unlock()
	}
	h.publish(b)
	return nil
}

func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".hammock-*")
	if err != nil {
		// A folder we can't create in: write in place.
		return os.WriteFile(path, data, mode)
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	_ = os.Chmod(tmp, mode)
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// read answers ReadEditor.
func (h *editorHub) read(m *pb.ReadEditor) (*pb.EditorUpdate, error) {
	h.mu.Lock()
	b, err := h.buf(m.PaneId)
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	u := &pb.EditorUpdate{PaneId: m.PaneId, Version: b.version, Snapshot: true, Text: b.text.String(), Git: b.marks, GitKnown: b.headKnown}
	if s, ok := b.sels[m.PaneId]; ok && s.version == b.version {
		u.Selection = &pb.EditorSelection{Anchor: s.anchor, Head: s.head}
	}
	path := b.path
	h.mu.Unlock()
	if m.Disk && path != "" {
		data, _, err := readTextFile(path, true)
		if errors.Is(err, fs.ErrNotExist) {
			u.DiskMissing = true
		} else if err != nil {
			return nil, err
		} else {
			t, _ := normaliseText(data)
			u.DiskText = t.String()
		}
	}
	return u, nil
}

// ---- the file on disk ----

// checkDisk notices the file changing: reloaded when nothing's unsaved,
// else a conflict.
func (h *editorHub) checkDisk(b *editorBuf) {
	h.mu.Lock()
	path, mod, size, missing := b.path, b.diskMod, b.diskSize, b.missing
	h.mu.Unlock()
	if path == "" {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !missing {
			h.mu.Lock()
			b.missing = true
			b.changed()
			h.mu.Unlock()
			h.publish(b)
		}
		return
	}
	if !missing && st.ModTime().Equal(mod) && st.Size() == size {
		return
	}
	data, _, err := readTextFile(path, true)
	if err != nil {
		return
	}
	sum := sha256.Sum256(data)
	h.mu.Lock()
	b.diskMod, b.diskSize = st.ModTime(), st.Size()
	if sum == b.diskSum && !b.missing {
		h.mu.Unlock()
		return
	}
	t, eol := normaliseText(data)
	b.diskSum = sum
	b.readOnly = !writable(path)
	if !b.dirty {
		b.disk, b.eol, b.missing = t, eol, false
		b.replaceText(t)
		b.changed()
	} else if slices.Equal(t, b.text) {
		b.disk, b.eol, b.missing, b.conflict = t, eol, false, false
		b.changed()
	} else {
		// Unsaved edits: the panes ask. The disk's text is what Reload takes.
		b.missing = false
		b.conflict = true
	}
	b.marksDue = true
	h.mu.Unlock()
	h.publish(b)
}

// keepUnsaved stores dirty buffers (so they outlive the daemon), and
// forgets clean ones.
func (h *editorHub) keepUnsaved(all bool) {
	h.mu.Lock()
	type rec struct {
		key string
		b   *editorBuf
		u   *pb.EditorUpdate
	}
	var put []rec
	var del []string
	for _, b := range h.bufs {
		if !b.saveDue && !all {
			continue
		}
		b.saveDue = false
		if b.dirty {
			put = append(put, rec{b.key, b, &pb.EditorUpdate{Text: b.text.String(), DiskText: b.disk.String(), DiskMissing: b.missing}})
		} else {
			del = append(del, b.key)
		}
	}
	h.mu.Unlock()
	for _, r := range put {
		h.d.persist(h.d.store.PutRecord(editorRecord, r.key, r.u))
	}
	for _, k := range del {
		_ = h.d.store.DeleteRecord(editorRecord, k)
	}
	// A buffer closed (or saved under another name) while that was written
	// deleted its record first: delete it again, or its discarded edits
	// would come back with the next pane on that file after a restart.
	var gone []string
	h.mu.Lock()
	for _, r := range put {
		if o := h.bufs[r.key]; o != r.b {
			gone = append(gone, r.key)
			if o != nil {
				o.saveDue = true // another buffer on that file now: kept again next time
			}
		}
	}
	h.mu.Unlock()
	for _, k := range gone {
		_ = h.d.store.DeleteRecord(editorRecord, k)
	}
}

// pruneRecords forgets kept buffers no editor pane has (left by a crash, or
// a pane closed as it was being kept), after restoring the panes.
func (h *editorHub) pruneRecords(saved map[string][]byte) {
	h.mu.Lock()
	var gone []string
	for k := range saved {
		if h.bufs[k] == nil {
			gone = append(gone, k)
		}
	}
	h.mu.Unlock()
	for _, k := range gone {
		_ = h.d.store.DeleteRecord(editorRecord, k)
	}
}

// ---- git ----

// loadHead reads the file as git's HEAD has it.
func (h *editorHub) loadHead(b *editorBuf) {
	h.mu.Lock()
	path := b.path
	h.mu.Unlock()
	var head []string
	known := false
	if path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		dir := filepath.Dir(path)
		top, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
		if err == nil {
			root := strings.TrimSpace(string(top))
			if r, err := filepath.EvalSymlinks(root); err == nil {
				root = r
			}
			if rel, err := filepath.Rel(root, path); err == nil {
				out, err := exec.CommandContext(ctx, "git", "-C", root, "show", "HEAD:"+filepath.ToSlash(rel)).Output()
				if err == nil {
					t, _ := normaliseText(out)
					head, known = textbuf.Lines(t), true
				}
			}
		}
	}
	h.mu.Lock()
	b.head, b.headKnown, b.marksDue = head, known, true
	h.mu.Unlock()
}

// refreshMarks recomputes the git marks if the text changed, sending them
// when they differ.
func (h *editorHub) refreshMarks(b *editorBuf, force bool) {
	h.mu.Lock()
	if !b.marksDue && !force {
		h.mu.Unlock()
		return
	}
	b.marksDue = false
	if !b.headKnown {
		had := len(b.marks) > 0
		b.marks = nil
		if had {
			b.broadcast(&pb.EditorUpdate{Version: b.version, GitKnown: false})
		}
		h.mu.Unlock()
		return
	}
	head, text := b.head, b.text
	h.mu.Unlock()
	marks := gitMarks(head, textbuf.Lines(text))
	h.mu.Lock()
	defer h.mu.Unlock()
	if marksEqual(marks, b.marks) {
		return
	}
	b.marks = marks
	b.broadcast(&pb.EditorUpdate{Version: b.version, Git: marks, GitKnown: true})
}

func gitMarks(head, cur []string) []*pb.GitMark {
	var out []*pb.GitMark
	for _, c := range textbuf.DiffLines(head, cur) {
		switch {
		case c.OldFrom == c.OldTo:
			out = append(out, &pb.GitMark{Kind: pb.GitMark_KIND_ADDED, From: uint32(c.NewFrom + 1), To: uint32(c.NewTo)})
		case c.NewFrom == c.NewTo:
			out = append(out, &pb.GitMark{Kind: pb.GitMark_KIND_DELETED, From: uint32(c.NewFrom), To: uint32(c.NewFrom)})
		default:
			out = append(out, &pb.GitMark{Kind: pb.GitMark_KIND_MODIFIED, From: uint32(c.NewFrom + 1), To: uint32(c.NewTo)})
		}
	}
	return out
}

func marksEqual(a, b []*pb.GitMark) bool {
	return slices.EqualFunc(a, b, func(x, y *pb.GitMark) bool { return proto.Equal(x, y) })
}

// ---- indentation ----

// indentFor is how path indents: its .editorconfig, else what its text
// mostly uses.
func indentFor(path string, text textbuf.Text) (uint32, string) {
	if path != "" {
		if w, style, ok := editorConfig(path); ok {
			return w, style
		}
	}
	tabs, spaces := 0, 0
	widths := map[int]int{}
	prev := 0
	for _, l := range textbuf.Lines(text) {
		if strings.HasPrefix(l, "\t") {
			tabs++
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " "))
		if n == len(l) {
			continue // blank
		}
		if n > 0 {
			spaces++
		}
		if d := n - prev; d >= 2 && d <= 8 {
			widths[d]++
		}
		prev = n
	}
	switch {
	case tabs > spaces && tabs > 0:
		return 0, "tabs"
	case spaces > 0:
		best, bn := 0, 0
		for w, n := range widths {
			if n > bn || n == bn && w < best {
				best, bn = w, n
			}
		}
		return uint32(best), "spaces"
	}
	return 0, ""
}

// editorConfig reads the indentation .editorconfig files give path.
func editorConfig(path string) (width uint32, style string, found bool) {
	var files []string
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		f := filepath.Join(dir, ".editorconfig")
		if data, err := os.ReadFile(f); err == nil {
			files = append(files, f)
			if regexpRoot(data) {
				break
			}
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	// Nearest last: it wins.
	for i := len(files) - 1; i >= 0; i-- {
		data, _ := os.ReadFile(files[i])
		rel, _ := filepath.Rel(filepath.Dir(files[i]), path)
		section := false
		for _, raw := range strings.Split(string(data), "\n") {
			l := strings.TrimSpace(raw)
			if l == "" || l[0] == '#' || l[0] == ';' {
				continue
			}
			if l[0] == '[' && strings.HasSuffix(l, "]") {
				section = ecMatch(l[1:len(l)-1], filepath.ToSlash(rel))
				continue
			}
			if !section {
				continue
			}
			k, v, ok := strings.Cut(l, "=")
			if !ok {
				continue
			}
			k, v = strings.ToLower(strings.TrimSpace(k)), strings.ToLower(strings.TrimSpace(v))
			switch k {
			case "indent_style":
				if v == "tab" {
					style, found = "tabs", true
				} else if v == "space" {
					style, found = "spaces", true
				}
			case "indent_size", "tab_width":
				var n uint32
				if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 && n <= 16 {
					width, found = n, true
				}
			}
		}
	}
	return width, style, found
}

func regexpRoot(data []byte) bool {
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(l), " ", ""))
		if l == "root=true" {
			return true
		}
		if strings.HasPrefix(l, "[") {
			return false
		}
	}
	return false
}

// ecMatch matches an .editorconfig section's glob (*, **, ?, {a,b}) against
// rel; one without a slash matches the name at any depth.
func ecMatch(glob, rel string) bool {
	for _, g := range expandBraces(glob) {
		subject := rel
		if !strings.Contains(g, "/") {
			subject = filepath.Base(rel)
		} else {
			g = strings.TrimPrefix(g, "/")
		}
		if globMatch(g, subject) {
			return true
		}
	}
	return false
}

func expandBraces(g string) []string {
	i := strings.IndexByte(g, '{')
	if i < 0 {
		return []string{g}
	}
	j := strings.IndexByte(g[i:], '}')
	if j < 0 {
		return []string{g}
	}
	var out []string
	for _, alt := range strings.Split(g[i+1:i+j], ",") {
		out = append(out, expandBraces(g[:i]+alt+g[i+j+1:])...)
	}
	return out
}

// globMatch: * within a path segment, ** across them, ? one character.
func globMatch(g, s string) bool {
	if g == "" {
		return s == ""
	}
	switch {
	case strings.HasPrefix(g, "**"):
		rest := strings.TrimPrefix(g[2:], "/")
		for i := 0; i <= len(s); i++ {
			if globMatch(rest, s[i:]) {
				return true
			}
		}
		return false
	case g[0] == '*':
		for i := 0; i <= len(s); i++ {
			if i > 0 && s[i-1] == '/' {
				break
			}
			if globMatch(g[1:], s[i:]) {
				return true
			}
		}
		return false
	case s == "":
		return false
	case g[0] == '?' && s[0] != '/', g[0] == s[0]:
		return globMatch(g[1:], s[1:])
	}
	return false
}

// ---- quick open ----

type fileListing struct {
	at        time.Time
	paths     []string
	truncated bool
}

const maxListed = 50000

// listFiles answers ListFiles for folder.
func (h *editorHub) listFiles(folder, query string, limit int) (*pb.FileList, error) {
	if folder == "" {
		return nil, errors.New("this Group has no folder")
	}
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	h.lmu.Lock()
	l := h.lists[folder]
	if l == nil || time.Since(l.at) > 5*time.Second {
		h.lmu.Unlock()
		paths, trunc := scanFiles(folder)
		l = &fileListing{at: time.Now(), paths: paths, truncated: trunc}
		h.lmu.Lock()
		h.lists[folder] = l
	}
	h.lmu.Unlock()
	return &pb.FileList{Folder: folder, Paths: rankFiles(l.paths, query, limit), Truncated: l.truncated}, nil
}

// scanFiles lists folder's files: git's (tracked, and untracked but not
// ignored) in a repository, else a walk.
func scanFiles(folder string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", folder, "ls-files", "-co", "--exclude-standard", "-z")
	if out, err := cmd.Output(); err == nil {
		var paths []string
		seen := map[string]bool{}
		for _, p := range strings.Split(string(out), "\x00") {
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			paths = append(paths, p)
			if len(paths) >= maxListed {
				return paths, true
			}
		}
		return paths, false
	}
	var paths []string
	trunc := false
	_ = filepath.WalkDir(folder, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := e.Name()
		if e.IsDir() {
			if p != folder && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(folder, p); err == nil {
			paths = append(paths, filepath.ToSlash(rel))
		}
		if len(paths) >= maxListed {
			trunc = true
			return filepath.SkipAll
		}
		return nil
	})
	return paths, trunc
}

// rankFiles picks the best limit of paths for query: its characters in
// order (case aside), scoring matches in the file's name, at word starts
// and in runs higher, and shorter paths first.
func rankFiles(paths []string, query string, limit int) []string {
	q := strings.ToLower(strings.ReplaceAll(query, " ", ""))
	type scored struct {
		p string
		s int
	}
	var out []scored
	for _, p := range paths {
		s, ok := fuzzyScore(p, q)
		if ok {
			out = append(out, scored{p, s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].s != out[j].s {
			return out[i].s > out[j].s
		}
		if len(out[i].p) != len(out[j].p) {
			return len(out[i].p) < len(out[j].p)
		}
		return out[i].p < out[j].p
	})
	res := make([]string, 0, min(limit, len(out)))
	for i := 0; i < len(out) && i < limit; i++ {
		res = append(res, out[i].p)
	}
	return res
}

func fuzzyScore(path, q string) (int, bool) {
	if q == "" {
		return -strings.Count(path, "/") * 10, true
	}
	lp := strings.ToLower(path)
	base := strings.LastIndexByte(lp, '/') + 1
	// Whole query in the name scores best.
	if i := strings.Index(lp[base:], q); i >= 0 {
		s := 1000 - i
		if i == 0 {
			s += 200
		}
		if name := lp[base:]; name == q || strings.HasPrefix(name, q+".") {
			s += 300 // the whole name (bar its extension)
		}
		return s, true
	}
	score, qi, run := 0, 0, 0
	for i := 0; i < len(lp) && qi < len(q); i++ {
		if lp[i] != q[qi] {
			run = 0
			continue
		}
		pts := 1 + run*3
		if i == 0 || strings.ContainsRune("/_-. ", rune(lp[i-1])) || (path[i] >= 'A' && path[i] <= 'Z') {
			pts += 6
		}
		if i >= base {
			pts += 4
		}
		score += pts
		run++
		qi++
	}
	if qi < len(q) {
		return 0, false
	}
	return score, true
}

// folderOf is a Group's folder (groupFolder; "" if there's no such Group).
// Callers hold d.mu.
func (d *Daemon) folderOf(groupID string) string {
	if g := d.groups[groupID]; g != nil {
		return d.groupFolder(g)
	}
	return ""
}

// guestEditorPath refuses a guest an editor pane on a file outside the
// Group's folder, or a diff pane on a repository outside it. Callers hold d.mu.
func (d *Daemon) guestEditorPath(spec *pb.PaneSpec, groupID string) error {
	if df := spec.GetDiff(); df != nil {
		folder := d.folderOf(groupID)
		repo := df.Repo
		if repo == "" {
			repo = folder
		}
		path, err := resolveEditorPath(repo, folder)
		if err != nil || !insideFolder(path, folder) {
			return errors.New("guests open diffs only inside the Group's folder")
		}
		// The repository as a whole, too: a diff is all of it, and a
		// folder inside a repository isn't all of it.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		top, err := gitdiff.TopLevel(ctx, path)
		cancel()
		if err != nil {
			return err
		}
		if !insideFolder(top, folder) {
			return errors.New("guests open diffs only of repositories inside the Group's folder")
		}
		df.Repo, spec.Cwd = top, ""
	}
	open := spec.GetEditor()
	if open == nil || open.Path == "" {
		return nil
	}
	folder := d.folderOf(groupID)
	path, err := resolveEditorPath(open.Path, folder)
	if err != nil || !insideFolder(path, folder) {
		return errors.New("guests open files only inside the Group's folder")
	}
	// Relative to the folder, so the pane opens what was checked.
	open.Path, open.Cwd = path, ""
	return nil
}
