package vt

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// KittyFiles turns Kitty graphics images sent as a file path (t=f, or t=t
// for a temporary file) into images sent inline (t=d), in a pane's output
// before it reaches the daemon's mirror and its clients.
//
// A client on this Mac could open the path itself, but one elsewhere (a
// paired iPhone, a linked or guest computer) can't: the file is here. So
// the daemon reads it once and every client gets the image itself.
//
// Only what's plainly an image is read: a regular file of at most
// MaxImageFile bytes whose content is what the command says (a PNG for
// f=100, exactly width × height pixels for raw RGB or RGBA; checked after
// decompressing when it's compressed, o=z, and sent on still compressed).
// Anything else
// passes through as it came: a program's path to some other file never
// reaches a client as that file's bytes.
type KittyFiles struct {
	held []byte // a command (or the start of one) split across reads
}

// MaxImageFile is the largest image file read: big screenshots fit.
const MaxImageFile = 64 << 20

const (
	apcStart   = "\x1b_G"
	apcEnd     = "\x1b\\"
	maxKeys    = 512  // control data before the payload
	maxFileCmd = 8192 // a whole file command: its keys and a base64 path
)

// Rewrite returns data with file images inlined. It may hold back the end of
// data (a command not yet complete) until the next call; the bytes it
// returns are always safe to pass on.
func (k *KittyFiles) Rewrite(data []byte) []byte {
	if len(k.held) > 0 {
		data = append(k.held, data...)
		k.held = nil
	}
	if !bytes.Contains(data, []byte("\x1b")) {
		return data
	}
	var out []byte
	rest := data
	for {
		i := bytes.Index(rest, []byte(apcStart))
		if i < 0 {
			// Hold back a trailing ESC or ESC _ that may start a command.
			if n := partialPrefix(rest, apcStart); n > 0 {
				k.held = append([]byte(nil), rest[len(rest)-n:]...)
				rest = rest[:len(rest)-n]
			}
			if out == nil {
				return rest
			}
			return append(out, rest...)
		}
		out = append(out, rest[:i]...)
		cmd := rest[i:]
		end := bytes.Index(cmd, []byte(apcEnd))
		semi := bytes.IndexByte(cmd, ';')
		keysEnd := semi
		if keysEnd < 0 || (end >= 0 && end < keysEnd) {
			keysEnd = end
		}
		if keysEnd < 0 {
			// The keys aren't all here yet.
			if len(cmd) <= maxKeys {
				k.held = append([]byte(nil), cmd...)
				return out
			}
			out = append(out, cmd...) // not a command we'd rewrite
			return out
		}
		keys := string(cmd[len(apcStart):keysEnd])
		if !isFileImage(keys) || keysEnd == end {
			// Not a file image: pass it through, from its start to the
			// end of its keys (its payload follows as it comes).
			out = append(out, cmd[:keysEnd]...)
			rest = cmd[keysEnd:]
			continue
		}
		if end < 0 {
			if len(cmd) <= maxFileCmd {
				k.held = append([]byte(nil), cmd...)
				return out
			}
			out = append(out, cmd...)
			return out
		}
		whole := cmd[:end+len(apcEnd)]
		if inline, ok := inlineImage(keys, string(cmd[semi+1:end])); ok {
			out = append(out, inline...)
		} else {
			out = append(out, whole...)
		}
		rest = cmd[end+len(apcEnd):]
	}
}

// partialPrefix is how many bytes at the end of b begin prefix.
func partialPrefix(b []byte, prefix string) int {
	for n := min(len(prefix)-1, len(b)); n > 0; n-- {
		if string(b[len(b)-n:]) == prefix[:n] {
			return n
		}
	}
	return 0
}

// isFileImage says whether a command's keys send an image from a file.
func isFileImage(keys string) bool {
	t := keyValue(keys, "t")
	return t == "f" || t == "t"
}

func keyValue(keys, key string) string {
	for _, kv := range strings.Split(keys, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// inlineImage reads the file a command names and returns the command as
// inline chunks (as programs send them: 4096 base64 bytes each), or false
// to leave it as it is.
func inlineImage(keys, payload string) ([]byte, bool) {
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	path := string(raw)
	if o := keyValue(keys, "o"); o != "" && o != "z" {
		return nil, false
	}
	temporary := keyValue(keys, "t") == "t"
	if temporary && !kittyTempFile(path) {
		return nil, false // the mirror refuses it too, as Kitty does
	}
	data, ok := readImageFile(path, keys)
	if !ok {
		return nil, false
	}
	if temporary {
		_ = os.Remove(path) // a temporary file is the terminal's to delete once read
	}

	var kept []string
	for _, kv := range strings.Split(keys, ",") {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "t", "S", "O", "m", "":
		default:
			kept = append(kept, kv)
		}
	}
	quiet := keyValue(keys, "q")
	b64 := base64.StdEncoding.EncodeToString(data)
	var out bytes.Buffer
	for first := true; first || len(b64) > 0; first = false {
		chunk := b64[:min(4096, len(b64))]
		b64 = b64[len(chunk):]
		more := "0"
		if len(b64) > 0 {
			more = "1"
		}
		out.WriteString(apcStart)
		if first {
			out.WriteString(strings.Join(append(kept, "t=d", "m="+more), ","))
		} else {
			if quiet != "" {
				out.WriteString("q=" + quiet + ",")
			}
			out.WriteString("m=" + more)
		}
		out.WriteByte(';')
		out.WriteString(chunk)
		out.WriteString(apcEnd)
	}
	return out.Bytes(), true
}

// readImageFile reads a regular file (or the part the command names, O
// and S) if it's what the command says it is.
func readImageFile(path string, keys string) ([]byte, bool) {
	if !filepath.IsAbs(path) {
		return nil, false
	}
	st, err := os.Stat(path) // follows a link: what it points at must be a file too
	if err != nil || !st.Mode().IsRegular() {
		return nil, false
	}
	offset, _ := strconv.ParseInt(keyValue(keys, "O"), 10, 64)
	size, _ := strconv.ParseInt(keyValue(keys, "S"), 10, 64)
	if offset < 0 || size < 0 || offset > st.Size() {
		return nil, false
	}
	if size == 0 || offset+size > st.Size() {
		size = st.Size() - offset
	}
	if size <= 0 || size > MaxImageFile {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data := make([]byte, size)
	if _, err := f.ReadAt(data, offset); err != nil && err != io.EOF {
		return nil, false
	}
	pixels := data
	if keyValue(keys, "o") == "z" {
		z, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false
		}
		pixels, err = io.ReadAll(io.LimitReader(z, 4*MaxImageFile+1))
		if err != nil || len(pixels) > 4*MaxImageFile {
			return nil, false
		}
	}
	switch format := keyValue(keys, "f"); format {
	case "100":
		return data, bytes.HasPrefix(pixels, []byte("\x89PNG\r\n\x1a\n"))
	case "24", "32", "":
		w, _ := strconv.Atoi(keyValue(keys, "s"))
		h, _ := strconv.Atoi(keyValue(keys, "v"))
		bpp := 4
		if format == "24" {
			bpp = 3
		}
		return data, w > 0 && h > 0 && len(pixels) == w*h*bpp
	}
	return nil, false
}

// kittyTempFile is Kitty's rule for t=t: a file in a temporary directory
// whose name says it's for this protocol (so a program can't have the
// terminal delete just anything).
func kittyTempFile(path string) bool {
	if !strings.Contains(filepath.Base(path), "tty-graphics-protocol") {
		return false
	}
	dir := filepath.Dir(path)
	for _, tmp := range []string{os.TempDir(), "/tmp", "/private/tmp", "/dev/shm"} {
		if t := filepath.Clean(tmp); dir == t || strings.HasPrefix(dir, t+"/") {
			return true
		}
	}
	return false
}
