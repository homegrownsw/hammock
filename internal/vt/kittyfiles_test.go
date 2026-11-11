package vt

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A tiny but real PNG header; KittyFiles only checks the signature.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 100)...)

func fileCmd(keys, path string) string {
	return "\x1b_G" + keys + ";" + base64.StdEncoding.EncodeToString([]byte(path)) + "\x1b\\"
}

// payloadOf joins the payloads of the commands in s and the keys of the first.
func payloadOf(t *testing.T, s string) (firstKeys string, data []byte) {
	t.Helper()
	var b64 strings.Builder
	for i, m := range regexp.MustCompile("\x1b_G([^;]*);([^\x1b]*)\x1b\\\\").FindAllStringSubmatch(s, -1) {
		if i == 0 {
			firstKeys = m[1]
		}
		b64.WriteString(m[2])
	}
	data, err := base64.StdEncoding.DecodeString(b64.String())
	if err != nil {
		t.Fatalf("payload isn't base64: %v", err)
	}
	return firstKeys, data
}

func compressed(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(data)
	w.Close()
	return writeFile(t, dir, name, b.Bytes())
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKittyFilesInlinesAPNG(t *testing.T) {
	p := writeFile(t, t.TempDir(), "shot.png", pngBytes)
	var k KittyFiles
	out := string(k.Rewrite([]byte("before " + fileCmd("a=T,f=100,t=f,c=10,r=5", p) + " after")))
	if strings.Contains(out, base64.StdEncoding.EncodeToString([]byte(p))) {
		t.Fatalf("the path is still there: %q", out)
	}
	if !strings.HasPrefix(out, "before ") || !strings.HasSuffix(out, " after") {
		t.Fatalf("the text around it changed: %q", out)
	}
	keys, data := payloadOf(t, out)
	if !bytes.Equal(data, pngBytes) {
		t.Fatalf("inlined %d bytes, want the file's %d", len(data), len(pngBytes))
	}
	for _, want := range []string{"a=T", "f=100", "c=10", "r=5", "t=d", "m=0"} {
		if !strings.Contains(","+keys+",", ","+want+",") {
			t.Errorf("keys %q lack %s", keys, want)
		}
	}
}

func TestKittyFilesLeavesWhatIsntAnImage(t *testing.T) {
	dir := t.TempDir()
	secret := writeFile(t, dir, "id_ed25519", []byte("-----BEGIN OPENSSH PRIVATE KEY-----"))
	raw := writeFile(t, dir, "pixels", make([]byte, 2*2*4))
	for name, in := range map[string]string{
		"not a PNG":        fileCmd("a=T,f=100,t=f", secret),
		"raw, wrong size":  fileCmd("a=T,f=32,s=3,v=2,t=f", raw),
		"compressed text":  fileCmd("a=T,f=100,o=z,t=f", compressed(t, dir, "z", []byte("not an image"))),
		"other compressor": fileCmd("a=T,f=100,o=x,t=f", secret),
		"a directory":      fileCmd("a=T,f=100,t=f", dir),
		"relative path":    fileCmd("a=T,f=100,t=f", "id_ed25519"),
		"missing":          fileCmd("a=T,f=100,t=f", filepath.Join(dir, "nope.png")),
		"inline already":   "\x1b_Ga=T,f=100,m=1;" + base64.StdEncoding.EncodeToString(pngBytes[:30]) + "\x1b\\",
		"not graphics APC": "\x1b_Xsomething\x1b\\",
	} {
		var k KittyFiles
		if out := string(k.Rewrite([]byte(in))); out != in {
			t.Errorf("%s: changed to %q", name, out)
		}
	}
	// The right size for raw RGBA is inlined, and so is a compressed PNG,
	// still compressed.
	var k KittyFiles
	z := compressed(t, dir, "zpng", pngBytes)
	if out := string(k.Rewrite([]byte(fileCmd("a=T,f=100,o=z,t=f", z)))); !strings.Contains(out, "o=z") || !strings.Contains(out, "t=d") {
		t.Errorf("a compressed PNG wasn't inlined compressed: %q", out)
	}
	if out := string(k.Rewrite([]byte(fileCmd("a=T,f=32,s=2,v=2,t=f", raw)))); !strings.Contains(out, "t=d") {
		t.Errorf("raw RGBA of the right size wasn't inlined: %q", out)
	}
}

// However the output is split across reads, what comes out is the same.
func TestKittyFilesAcrossReads(t *testing.T) {
	p := writeFile(t, t.TempDir(), "shot.png", pngBytes)
	in := "x\x1b[1mbold\x1b[0m " + fileCmd("a=T,f=100,t=f", p) + " y " + fileCmd("a=T,f=100,t=f", p) + "\x1b"
	var whole KittyFiles
	want := string(whole.Rewrite([]byte(in))) + string(whole.Rewrite([]byte("[0m")))
	for i := 1; i < len(in); i++ {
		var k KittyFiles
		got := string(k.Rewrite([]byte(in[:i]))) + string(k.Rewrite([]byte(in[i:]))) + string(k.Rewrite([]byte("[0m")))
		if got != want {
			t.Fatalf("split at %d: %q, want %q", i, got, want)
		}
	}
}

func TestKittyFilesChunksBigImages(t *testing.T) {
	big := append(append([]byte(nil), pngBytes...), bytes.Repeat([]byte{9}, 20000)...)
	p := writeFile(t, t.TempDir(), "big.png", big)
	var k KittyFiles
	out := string(k.Rewrite([]byte(fileCmd("a=T,f=100,t=f,q=2", p))))
	cmds := strings.Count(out, "\x1b_G")
	if cmds < 7 || !strings.Contains(out, "\x1b_Gq=2,m=1;") || !strings.Contains(out, "\x1b_Gq=2,m=0;") {
		t.Fatalf("%d commands, not chunked as programs chunk them", cmds)
	}
	if _, data := payloadOf(t, out); !bytes.Equal(data, big) {
		t.Fatal("the chunks don't add up to the file")
	}
}

func TestKittyFilesTemporaryFiles(t *testing.T) {
	tmp := writeFile(t, os.TempDir(), "tty-graphics-protocol-test.png", pngBytes)
	var k KittyFiles
	if out := string(k.Rewrite([]byte(fileCmd("a=T,f=100,t=t", tmp)))); !strings.Contains(out, "t=d") {
		t.Fatalf("a temporary file wasn't inlined: %q", out)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("a temporary file is the terminal's to delete once read")
	}
	// Not named as one, or not in a temporary directory: left alone, and kept.
	kept := writeFile(t, t.TempDir(), "shot.png", pngBytes)
	in := fileCmd("a=T,f=100,t=t", kept)
	if out := string(k.Rewrite([]byte(in))); out != in {
		t.Fatalf("inlined a t=t file that isn't Kitty's kind of temporary: %q", out)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatal("deleted a file that wasn't a temporary one")
	}
}
