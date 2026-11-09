package daemon

// Markdown for hmk show: rendered to a page kept with the daemon's data
// (run/show/), styled for light and dark, so a browser pane shows it read.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

const showCSS = `:root{color-scheme:light dark}body{font:15px/1.55 -apple-system,BlinkMacSystemFont,sans-serif;max-width:760px;margin:32px auto;padding:0 20px;color:#1d1d1f;background:#fff}
@media (prefers-color-scheme:dark){body{color:#f2f2f7;background:#1c1c1e}a{color:#8f8cff}code,pre{background:#2c2c2e!important}th,td{border-color:#3a3a3c!important}}
a{color:#4b48c9}code{font:13px ui-monospace,Menlo,monospace;background:#f2f2f7;padding:1px 4px;border-radius:4px}pre{background:#f2f2f7;padding:12px;border-radius:8px;overflow:auto}pre code{padding:0}
table{border-collapse:collapse}th,td{border:1px solid #d2d2d7;padding:4px 8px}img{max-width:100%}h1,h2,h3{line-height:1.25}`

// renderMarkdownFile renders path to an HTML page and returns the page's path.
func (d *Daemon) renderMarkdownFile(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > 8<<20 {
		return "", fmt.Errorf("%s isn't a file of 8 MB or less", path)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	if err := md.Convert(src, &body); err != nil {
		return "", err
	}
	dir := filepath.Join(d.cfg.Dir, "run", "show")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(path))
	out := filepath.Join(dir, hex.EncodeToString(sum[:8])+".html")
	// Relative links and images resolve from the Markdown file's folder.
	base := fileURL(filepath.Dir(path) + "/")
	page := "<!doctype html><meta charset=utf-8><base href=\"" + html.EscapeString(base) + "\"><title>" +
		html.EscapeString(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))) + "</title><style>" + showCSS + "</style>" +
		"<!-- " + time.Now().Format(time.RFC3339Nano) + " -->" + body.String()
	if err := os.WriteFile(out, []byte(page), 0o600); err != nil {
		return "", err
	}
	return out, nil
}
