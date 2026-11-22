package daemon

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// makeFiles writes each file (relative to root), with its folders.
func makeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, text := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestFindVault(t *testing.T) {
	root := realDir(t)
	os.MkdirAll(filepath.Join(root, "vault", ".obsidian"), 0o755)
	os.MkdirAll(filepath.Join(root, "vault", "Projects", "Notes"), 0o755)
	os.MkdirAll(filepath.Join(root, "loose", "sub"), 0o755)
	if got := findVault(filepath.Join(root, "vault", "Projects", "Notes")); got != filepath.Join(root, "vault") {
		t.Errorf("nested note's vault: %s", got)
	}
	if got := findVault(filepath.Join(root, "vault")); got != filepath.Join(root, "vault") {
		t.Errorf("vault's own folder: %s", got)
	}
	if got := findVault(filepath.Join(root, "loose", "sub")); got != filepath.Join(root, "loose", "sub") {
		t.Errorf("without a vault, the note's folder: %s", got)
	}
}

func TestResolveVaultLink(t *testing.T) {
	root := realDir(t)
	vault := filepath.Join(root, "vault")
	makeFiles(t, vault, map[string]string{
		".obsidian/app.json":            "{}",
		"Garden Plan.md":                "top",
		"Projects/Garden Plan.md":       "nearer",
		"Projects/Notes/Seeds.md":       "seeds",
		"Projects/Notes/Seeds.txt":      "not the note",
		"Images/diagram.svg":            "<svg/>",
		"Archive/report.pdf":            "%PDF",
		"Archive/v1.2 notes.md":         "dotted",
		"Makefile":                      "all:",
		".trash/Old.md":                 "trashed",
		".git/Head.md":                  "git's",
		".hidden/Secret.md":             "hidden",
		"node_modules/pkg/Readme.md":    "package",
		"Projects/.dotfile.md":          "hidden file",
		"Projects/Notes/Deep/Leaf.md":   "leaf",
		"Elsewhere/Deep/Leaf.md":        "other leaf",
		"Elsewhere/Far/Away/Cousin.md":  "cousin",
		"Projects/Notes/Nested/Twin.md": "near twin",
		"Twin/A/B/C/Twin.md":            "far twin",
	})
	makeFiles(t, root, map[string]string{"outside.md": "outside the vault"})
	idx := listVault(vault)
	all := func(string) bool { return true }
	at := func(rel string) string { return filepath.Join(vault, filepath.FromSlash(rel)) }
	notes := at("Projects/Notes")
	cases := []struct {
		from, target, want string
	}{
		{vault, "Garden Plan", "Garden Plan.md"},
		{notes, "Garden Plan", "Projects/Garden Plan.md"}, // the nearest of two
		{at("Elsewhere"), "Garden Plan", "Garden Plan.md"},
		{vault, "Garden Plan.md", "Garden Plan.md"},                // .md written out
		{vault, "garden plan", "Garden Plan.md"},                   // any case, given in the vault's
		{vault, "Seeds", "Projects/Notes/Seeds.md"},                // a note before a file of that name
		{vault, "Seeds.txt", "Projects/Notes/Seeds.txt"},           // another extension, as written
		{vault, "diagram.svg", "Images/diagram.svg"},               // anywhere in the vault
		{vault, "report.pdf", "Archive/report.pdf"},                //
		{vault, "v1.2 notes", "Archive/v1.2 notes.md"},             // a dot that isn't an extension
		{vault, "Makefile", "Makefile"},                            // no extension, no note: the file
		{vault, "Projects/Garden Plan", "Projects/Garden Plan.md"}, // a path from the vault's root
		{vault, "/Projects/Garden Plan", "Projects/Garden Plan.md"},
		{at("Projects"), "Notes/Seeds", "Projects/Notes/Seeds.md"}, // from the note's folder
		{vault, "Notes/Seeds", "Projects/Notes/Seeds.md"},          // a path's end
		{at("Elsewhere"), "Deep/Leaf", "Elsewhere/Deep/Leaf.md"},   // nearest of those it ends
		{notes, "Deep/Leaf", "Projects/Notes/Deep/Leaf.md"},
		{notes, "Twin", "Projects/Notes/Nested/Twin.md"},
		{at("Twin/A"), "Twin", "Twin/A/B/C/Twin.md"},
		{at("Elsewhere/Deep"), "Cousin", "Elsewhere/Far/Away/Cousin.md"},
		{vault, "Old", ""},    // .trash
		{vault, "Head", ""},   // .git
		{vault, "Secret", ""}, // a hidden folder
		{vault, "Readme", ""}, // node_modules
		{vault, ".dotfile", ""},
		{vault, "Nowhere", ""},
		{vault, "", ""},
		{vault, "../outside", ""}, // not outside the vault
		{at("Projects"), "../../outside", ""},
	}
	for _, c := range cases {
		got := resolveVaultLink(idx, c.from, c.target, all)
		want := ""
		if c.want != "" {
			want = at(c.want)
		}
		if got != want {
			t.Errorf("from %s, [[%s]]: %q, want %q", c.from, c.target, got, want)
		}
	}
	// What allow refuses isn't given; the next nearest is.
	notTop := func(p string) bool { return filepath.Dir(p) != vault }
	if got := resolveVaultLink(idx, vault, "Garden Plan", notTop); got != at("Projects/Garden Plan.md") {
		t.Errorf("with the nearest refused: %q", got)
	}

	// Without a vault, the note's folder and those in it.
	loose := filepath.Join(root, "loose")
	makeFiles(t, loose, map[string]string{"Note.md": "", "sub/Other.md": "", "sub/pic.png": ""})
	lidx := listVault(findVault(loose))
	if got := resolveVaultLink(lidx, loose, "Other", all); got != filepath.Join(loose, "sub", "Other.md") {
		t.Errorf("no vault, below: %q", got)
	}
	if got := resolveVaultLink(lidx, loose, "outside", all); got != "" {
		t.Errorf("no vault, above: %q", got)
	}
}

func TestLinkDistance(t *testing.T) {
	for _, c := range []struct {
		from, file string
		want       int
	}{
		{".", "a.md", 0},
		{".", "x/a.md", 1},
		{"x", "a.md", 1},
		{"x/y", "x/z/a.md", 2},
		{"x/y", "x/y/a.md", 0},
	} {
		if got := linkDistance(c.from, c.file); got != c.want {
			t.Errorf("linkDistance(%q, %q) = %d, want %d", c.from, c.file, got, c.want)
		}
	}
}

func TestVaultCache(t *testing.T) {
	vault := realDir(t)
	makeFiles(t, vault, map[string]string{".obsidian/app.json": "{}", "A.md": ""})
	c := &vaultCache{idx: map[string]*vaultIndex{}}
	first := c.get(vault, vaultFresh)
	makeFiles(t, vault, map[string]string{"sub/B.md": ""})
	if c.get(vault, vaultFresh) != first {
		t.Error("listed again while fresh")
	}
	if again := c.get(vault, 0); again == first || len(again.byName["b.md"]) != 1 {
		t.Error("not listed again once stale")
	}
	for i := 0; i < vaultCacheMax+3; i++ {
		c.get(realDir(t), vaultFresh)
	}
	if len(c.idx) > vaultCacheMax {
		t.Errorf("kept %d vaults", len(c.idx))
	}
}

func TestEditorLinks(t *testing.T) {
	dir := shortDir(t)
	folder := realDir(t)
	outside := realDir(t)
	makeFiles(t, folder, map[string]string{
		".obsidian/app.json":      "{}",
		"Home.md":                 "[[Garden Plan]] ![[Seeds]]\n",
		"Projects/Garden Plan.md": "# Garden Plan\n\n## Beds\n\nCarrots.\n",
		"Projects/Seeds.md":       "- [ ] Sow beans ^sow\n",
		"Projects/plan.svg":       "<svg/>",
		"Projects/big.md":         string(make([]byte, editorNoteMax+1)),
		"Projects/notes.txt":      "text",
		"Projects/Sub/Leaf.md":    "leaf",
	})
	makeFiles(t, outside, map[string]string{"Away.md": "away"})
	d := startDaemon(t, dir)
	c := dial(t, dir)
	_, home := newEditorTab(t, d, c, folder, "Home.md")
	resolve := func(m *pb.ResolveEditorLinks) (*pb.EditorLinks, error) {
		r, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ResolveEditorLinks{ResolveEditorLinks: m}})
		return r.GetEditorLinks(), err
	}
	r, err := resolve(&pb.ResolveEditorLinks{PaneId: home,
		Targets: []string{"Garden Plan", "Seeds", "plan.svg", "big", "notes.txt", "Missing"},
		Notes:   []string{"Seeds", "big", "notes.txt", "plan.svg"}})
	if err != nil || len(r.Links) != 6 {
		t.Fatalf("resolve: %v, %v", r, err)
	}
	want := []struct {
		path    string
		hasText bool
		text    string
	}{
		{"Projects/Garden Plan.md", false, ""}, // not asked for its text
		{"Projects/Seeds.md", true, "- [ ] Sow beans ^sow\n"},
		{"Projects/plan.svg", false, ""}, // not a note
		{"Projects/big.md", false, ""},   // too big
		{"Projects/notes.txt", false, ""},
		{"", false, ""},
	}
	for i, w := range want {
		l := r.Links[i]
		p := ""
		if w.path != "" {
			p = filepath.Join(folder, filepath.FromSlash(w.path))
		}
		if l.Path != p || l.HasText != w.hasText || l.Text != w.text {
			t.Errorf("%s: %q %v %q, want %q %v %q", l.Target, l.Path, l.HasText, l.Text, p, w.hasText, w.text)
		}
	}
	// From an embedded note: nearest to it.
	r, err = resolve(&pb.ResolveEditorLinks{PaneId: home, From: filepath.Join(folder, "Projects", "Seeds.md"), Targets: []string{"Leaf"}})
	if err != nil || r.Links[0].Path != filepath.Join(folder, "Projects", "Sub", "Leaf.md") {
		t.Errorf("from an embedded note: %v, %v", r, err)
	}
	if _, err := resolve(&pb.ResolveEditorLinks{PaneId: home, From: "Seeds.md"}); err == nil {
		t.Error("took a relative from")
	}
	// An open buffer's text, unsaved edits and all.
	_, seeds := newEditorTab(t, d, c, folder, "Projects/Seeds.md")
	_ = seeds
	d.editors.mu.Lock()
	if b := d.editors.bufs[filepath.Join(folder, "Projects", "Seeds.md")]; b != nil {
		b.text = []uint16{'e', 'd', 'i', 't', 'e', 'd'}
	}
	d.editors.mu.Unlock()
	r, _ = resolve(&pb.ResolveEditorLinks{PaneId: home, Targets: []string{"Seeds"}, Notes: []string{"Seeds"}})
	if r.Links[0].Text != "edited" {
		t.Errorf("an open note's text: %q", r.Links[0].Text)
	}

	// The owner, from a note outside the Group's folder.
	r, err = resolve(&pb.ResolveEditorLinks{PaneId: home, From: filepath.Join(outside, "Away.md"), Targets: []string{"Away"}, Notes: []string{"Away"}})
	if err != nil || r.Links[0].Text != "away" {
		t.Errorf("owner, outside the folder: %v, %v", r, err)
	}

	// A guest on Projects/Garden Plan.md: only beside it (Projects and below), in the Group's folder.
	_, plan := newEditorTab(t, d, c, folder, "Projects/Garden Plan.md")
	guest := func(from string, targets ...string) *pb.EditorLinks {
		t.Helper()
		m, err := d.resolveEditorLinks(&pb.ResolveEditorLinks{PaneId: plan, From: from, Targets: targets, Notes: targets}, folder, true)
		if err != nil {
			t.Fatal(err)
		}
		return m.GetEditorLinks()
	}
	g := guest("", "Seeds", "Leaf", "Home")
	if g.Links[0].Path == "" || g.Links[0].Text == "" || g.Links[1].Path == "" {
		t.Errorf("a guest, beside the file: %v", g)
	}
	if g.Links[2].Path != "" {
		t.Errorf("a guest got a note above the file: %v", g.Links[2])
	}
	for _, from := range []string{filepath.Join(folder, "Home.md"), filepath.Join(outside, "Away.md")} {
		if g := guest(from, "Away", "Home", "Seeds"); g.Links[0].Path != "" || g.Links[1].Path != "" || g.Links[2].Path != "" {
			t.Errorf("a guest, from %s: %v", from, g)
		}
	}
	// Even through a link in the folder to a note outside it.
	os.Symlink(filepath.Join(outside, "Away.md"), filepath.Join(folder, "Projects", "Linked.md"))
	if g := guest("", "Linked"); g.Links[0].Path != "" || g.Links[0].HasText {
		t.Errorf("a guest, through a link: %v", g)
	}
}
