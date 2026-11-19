package daemon

// Images for an editor pane's Markdown preview: the page shows them from
// the daemon, so they show wherever the pane does (the iPhone, a Group on
// another computer or shared with you), not only on the file's own Mac.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/regfile"
)

// editorImageMax is the largest image the preview is sent.
const editorImageMax = 16 << 20

// editorImageTypes are the files the preview may ask for, by extension.
var editorImageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".svg": "image/svg+xml", ".heic": "image/heic", ".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon",
}

// readEditorImage answers ReadEditorImage. guest: a guest's, only beside the
// pane's file (its folder and those in it) and in the Group's folder; else
// any image on this computer, as its owner may open.
func (d *Daemon) readEditorImage(m *pb.ReadEditorImage, folder string, guest bool) (*pb.ServerMessage, error) {
	h := d.editors
	h.mu.Lock()
	b, err := h.buf(m.PaneId)
	docDir := ""
	if err == nil && b.path != "" {
		docDir = filepath.Dir(b.path)
	}
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(m.Path) {
		return nil, errors.New("give an absolute path")
	}
	// Resolved (symlinks followed), so a link can't lead out, nor pass
	// another kind of file off as an image.
	path, err := resolveEditorPath(m.Path, "")
	if err != nil {
		return nil, err
	}
	typ, ok := editorImageTypes[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, fmt.Errorf("%s isn't an image", filepath.Base(m.Path))
	}
	if guest && !(insideFolder(path, folder) && insideFolder(path, docDir)) {
		return nil, errors.New("only images beside the file, in the Group's folder")
	}
	data, err := regfile.ReadFile(path, editorImageMax)
	if errors.Is(err, regfile.ErrNotRegular) || errors.Is(err, regfile.ErrTooLarge) {
		return nil, fmt.Errorf("%s isn't an image of 16 MB or less", filepath.Base(path))
	}
	if err != nil {
		return nil, err
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_EditorImage{EditorImage: &pb.EditorImage{Path: m.Path, MediaType: typ, Data: data}}}, nil
}
