// Command gen writes internal/acknowledgements/Acknowledgements.json: the
// third-party software each Hammock app ships, with each one's licence text,
// for the apps' Acknowledgements and `hmk acknowledgements`. Run by
// `just acknowledgements` from the repository's root:
//
//   - Go modules: those linked into ./cmd/... (hmk, hmkd, pane-holder) for
//     macOS (the Mac app) and Linux (the tarballs), from `go list -deps`.
//   - Swift packages: apple/Package.resolved's pins, from their checkouts.
//   - npm packages: those esbuild bundles into apple/Resources/editor.html.
//   - Code copied into the repo: THIRD_PARTY_NOTICES.md's table and sections.
//   - The rest (libghostty-vt and what it vendors, Go itself, CEF):
//     third_party/acknowledgements.json, with texts from their own files.
//
// The output is deterministic (no timestamps), so a diff means what ships
// changed. internal/acknowledgements' tests fail when it's out of date.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/homegrownsw/hammock/internal/acknowledgements"
)

type group struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	URL    string `json:"url"`
}

type manual struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	URL     string   `json:"url"`
	License string   `json:"license"`
	Apps    []string `json:"apps"`
	Note    string   `json:"note"`
	Credits string   `json:"credits"`
	Files   []string `json:"files"`
}

type manifest struct {
	Groups []group `json:"groups"`
	Swift  struct {
		Apps        map[string][]string `json:"apps"`
		DefaultApps []string            `json:"defaultApps"`
		Names       map[string]string   `json:"names"`
		Licenses    map[string]string   `json:"licenses"`
		Notes       map[string]string   `json:"notes"`
		Exclude     []string            `json:"exclude"`
	} `json:"swift"`
	Components []manual `json:"components"`
}

// pkg is one package found, before packages are grouped into components.
type pkg struct {
	name, version, url, license, note string
	apps                              []string
	texts                             []text
}

// text is one licence file's contents, with where it came from.
type text struct{ label, body string }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "acknowledgements:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	var m manifest
	if err := readJSON(filepath.Join(root, "third_party/acknowledgements.json"), &m); err != nil {
		return err
	}
	vars, err := variables(root)
	if err != nil {
		return err
	}

	var pkgs []pkg
	goPkgs, err := goModules(root)
	if err != nil {
		return err
	}
	pkgs = append(pkgs, goPkgs...)
	swiftPkgs, err := swiftPackages(root, &m)
	if err != nil {
		return err
	}
	pkgs = append(pkgs, swiftPkgs...)
	npmPkgs, err := npmPackages(root)
	if err != nil {
		return err
	}
	pkgs = append(pkgs, npmPkgs...)

	comps := group_(pkgs, m.Groups)
	for _, c := range m.Components {
		comp, err := manualComponent(c, vars)
		if err != nil {
			return err
		}
		comps = append(comps, comp)
	}
	notices, err := noticesComponents(filepath.Join(root, "THIRD_PARTY_NOTICES.md"))
	if err != nil {
		return err
	}
	comps = append(comps, notices...)

	sort.SliceStable(comps, func(i, j int) bool {
		key := func(s string) string { return strings.TrimPrefix(strings.ToLower(s), "@") }
		return key(comps[i].Name) < key(comps[j].Name)
	})
	for i := 1; i < len(comps); i++ {
		if strings.EqualFold(comps[i].Name, comps[i-1].Name) {
			return fmt.Errorf("two components are named %q", comps[i].Name)
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(acknowledgements.File{Components: comps}); err != nil {
		return err
	}
	out := filepath.Join(root, "internal/acknowledgements/Acknowledgements.json")
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d components, %d bytes\n", out, len(comps), buf.Len())
	return nil
}

// variables fills in third_party/acknowledgements.json's {placeholders}.
func variables(root string) (map[string]string, error) {
	pinned := func(file string) (string, error) {
		b, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return "", err
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				return l, nil
			}
		}
		return "", fmt.Errorf("%s pins nothing", file)
	}
	ghostty, err := pinned("third_party/ghostty/VERSION")
	if err != nil {
		return nil, err
	}
	cef, err := pinned("third_party/cef/VERSION")
	if err != nil {
		return nil, err
	}
	cache := os.Getenv("HMK_GHOSTTY_CACHE")
	if cache == "" {
		if runtime.GOOS == "darwin" {
			cache = filepath.Join(os.Getenv("HOME"), "Library/Caches/hammock/ghostty")
		} else if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
			cache = filepath.Join(x, "hammock/ghostty")
		} else {
			cache = filepath.Join(os.Getenv("HOME"), ".cache/hammock/ghostty")
		}
	}
	cefDir := filepath.Join(os.Getenv("HOME"), "Library/Caches/hammock/cef", cef)
	if d := os.Getenv("HMK_CEF_CACHE"); d != "" {
		cefDir = filepath.Join(d, cef)
	}
	goVersion, err := output(root, nil, "go", "env", "GOVERSION")
	if err != nil {
		return nil, err
	}
	goroot, err := output(root, nil, "go", "env", "GOROOT")
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"{repo}":        root,
		"{ghostty}":     ghostty,
		"{ghostty_src}": filepath.Join(cache, ghostty, "src"),
		"{cef}":         cef,
		"{cef_dir}":     cefDir,
		"{go}":          strings.TrimPrefix(strings.TrimSpace(goVersion), "go"),
		"{goroot}":      strings.TrimSpace(goroot),
	}, nil
}

// MARK: Go

// goModules lists the modules linked into ./cmd/... for macOS and Linux.
func goModules(root string) ([]pkg, error) {
	type mod struct{ path, version, dir string }
	found := map[string]*mod{}
	apps := map[string][]string{}
	for _, target := range []struct{ goos, app string }{{"darwin", "mac"}, {"linux", "linux"}} {
		out, err := output(root, []string{"GOOS=" + target.goos, "GOARCH=arm64", "CGO_ENABLED=1"},
			"go", "list", "-deps", "-tags", "ghostty",
			"-f", "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}", "./cmd/...")
		if err != nil {
			return nil, err
		}
		for _, l := range strings.Split(out, "\n") {
			f := strings.Split(l, "\t")
			if len(f) != 3 {
				continue
			}
			if _, ok := found[f[0]]; !ok {
				found[f[0]] = &mod{f[0], f[1], f[2]}
			}
			if !slices.Contains(apps[f[0]], target.app) {
				apps[f[0]] = append(apps[f[0]], target.app)
			}
		}
	}
	var pkgs []pkg
	for _, m := range found {
		texts, err := licenseFiles(m.dir, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.path, err)
		}
		pkgs = append(pkgs, pkg{
			name: m.path, version: m.version, url: "https://pkg.go.dev/" + m.path,
			license: classify(texts), apps: apps[m.path], texts: texts,
		})
	}
	return pkgs, nil
}

// MARK: Swift

func swiftPackages(root string, m *manifest) ([]pkg, error) {
	var resolved struct {
		Pins []struct {
			Identity string `json:"identity"`
			Location string `json:"location"`
			State    struct {
				Version  string `json:"version"`
				Revision string `json:"revision"`
			} `json:"state"`
		} `json:"pins"`
	}
	if err := readJSON(filepath.Join(root, "apple/Package.resolved"), &resolved); err != nil {
		return nil, err
	}
	checkouts := filepath.Join(root, "apple/.build/checkouts")
	dirOf := func(location string) string {
		return filepath.Join(checkouts, strings.TrimSuffix(filepath.Base(location), ".git"))
	}
	for _, p := range resolved.Pins {
		if _, err := os.Stat(dirOf(p.Location)); err != nil {
			if _, err := output(filepath.Join(root, "apple"), nil, "swift", "package", "resolve"); err != nil {
				return nil, err
			}
			break
		}
	}
	var pkgs []pkg
	for _, p := range resolved.Pins {
		dir := dirOf(p.Location)
		var skip []string
		for _, e := range m.Swift.Exclude {
			if id, file, ok := strings.Cut(e, "/"); ok && id == p.Identity {
				skip = append(skip, file)
			}
		}
		texts, err := licenseFiles(dir, skip)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Identity, err)
		}
		name := m.Swift.Names[p.Identity]
		if name == "" {
			name = p.Identity
		}
		apps := m.Swift.Apps[p.Identity]
		if apps == nil {
			apps = m.Swift.DefaultApps
		}
		license := m.Swift.Licenses[p.Identity]
		if license == "" {
			license = classify(texts)
		}
		version := p.State.Version
		if version == "" {
			version = p.State.Revision
		}
		pkgs = append(pkgs, pkg{
			name: name, version: version, url: strings.TrimSuffix(p.Location, ".git"),
			license: license, note: m.Swift.Notes[p.Identity], apps: apps, texts: texts,
		})
	}
	return pkgs, nil
}

// MARK: npm

// npmPackages lists the packages esbuild bundles into the editor's page, as
// apple/Editor/build.sh bundles it.
func npmPackages(root string) ([]pkg, error) {
	dir := filepath.Join(root, "apple/Editor")
	if _, err := os.Stat(filepath.Join(dir, "node_modules/.package-lock.json")); err != nil {
		if _, err := output(dir, nil, "npm", "ci", "--silent", "--no-audit", "--no-fund"); err != nil {
			return nil, err
		}
	}
	meta, err := os.CreateTemp("", "hmk-editor-meta-*.json")
	if err != nil {
		return nil, err
	}
	meta.Close()
	defer os.Remove(meta.Name())
	if _, err := output(dir, nil, "npm", "exec", "--", "esbuild", "src/main.js", "--bundle", "--format=iife",
		"--target=safari18", "--metafile="+meta.Name(), "--outfile="+os.DevNull, "--log-level=warning"); err != nil {
		return nil, err
	}
	var m struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
	}
	if err := readJSON(meta.Name(), &m); err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`^node_modules/((?:@[^/]+/)?[^/]+)/`)
	names := map[string]bool{}
	for in := range m.Inputs {
		if s := re.FindStringSubmatch(in); s != nil {
			names[s[1]] = true
		}
	}
	var pkgs []pkg
	for name := range names {
		pdir := filepath.Join(dir, "node_modules", name)
		var pj struct {
			Version  string `json:"version"`
			License  string `json:"license"`
			Homepage string `json:"homepage"`
		}
		if err := readJSON(filepath.Join(pdir, "package.json"), &pj); err != nil {
			return nil, err
		}
		texts, err := licenseFiles(pdir, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		url := pj.Homepage
		if url == "" {
			url = "https://www.npmjs.com/package/" + name
		}
		license := pj.License
		if license == "" {
			license = classify(texts)
		}
		pkgs = append(pkgs, pkg{
			name: name, version: pj.Version, url: url, license: license,
			apps: []string{"mac", "ios"}, texts: texts,
		})
	}
	return pkgs, nil
}

// MARK: Grouping

// group_ makes components of packages: one each, except those whose names
// start with a group's prefix, which become one component listing them all,
// with each different licence text once.
func group_(pkgs []pkg, groups []group) []acknowledgements.Component {
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].name < pkgs[j].name })
	byGroup := map[string][]pkg{}
	var comps []acknowledgements.Component
	for _, p := range pkgs {
		g := ""
		for _, gr := range groups {
			if strings.HasPrefix(p.name, gr.Prefix) {
				g = gr.Prefix
				break
			}
		}
		if g == "" {
			comps = append(comps, acknowledgements.Component{
				Name: p.name, Version: p.version, URL: p.url, License: p.license, Apps: sortedApps(p.apps),
				Note: p.note, Text: joinTexts(p.texts, false),
			})
			continue
		}
		byGroup[g] = append(byGroup[g], p)
	}
	for _, gr := range groups {
		members := byGroup[gr.Prefix]
		if len(members) == 0 {
			continue
		}
		c := acknowledgements.Component{Name: gr.Name, URL: gr.URL}
		var licenses, apps []string
		type shared struct {
			body  string
			names []string
		}
		var texts []*shared
		for _, p := range members {
			c.Packages = append(c.Packages, acknowledgements.Package{Name: p.name, Version: p.version})
			for _, l := range strings.Split(p.license, " AND ") {
				if !slices.Contains(licenses, l) {
					licenses = append(licenses, l)
				}
			}
			for _, a := range p.apps {
				if !slices.Contains(apps, a) {
					apps = append(apps, a)
				}
			}
			for _, t := range p.texts {
				label := p.name
				if len(p.texts) > 1 {
					label += " (" + t.label + ")"
				}
				i := slices.IndexFunc(texts, func(s *shared) bool { return s.body == t.body })
				if i < 0 {
					texts = append(texts, &shared{body: t.body})
					i = len(texts) - 1
				}
				texts[i].names = append(texts[i].names, label)
			}
		}
		if len(members) == 1 {
			c.Version = members[0].version
		}
		sort.Strings(licenses)
		c.License = strings.Join(licenses, " AND ")
		c.Apps = sortedApps(apps)
		var ts []text
		for _, s := range texts {
			ts = append(ts, text{label: strings.Join(s.names, ", "), body: s.body})
		}
		c.Text = joinTexts(ts, len(ts) > 1 || len(members) > 1)
		comps = append(comps, c)
	}
	return comps
}

func joinTexts(ts []text, labelled bool) string {
	labelled = labelled || len(ts) > 1
	var b strings.Builder
	for i, t := range ts {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if labelled {
			b.WriteString("── " + t.label + " ──\n\n")
		}
		b.WriteString(t.body)
	}
	return b.String()
}

func sortedApps(apps []string) []string {
	order := []string{"mac", "ios", "linux"}
	out := slices.Clone(apps)
	sort.Slice(out, func(i, j int) bool { return slices.Index(order, out[i]) < slices.Index(order, out[j]) })
	return out
}

// MARK: Hand-listed

func manualComponent(c manual, vars map[string]string) (acknowledgements.Component, error) {
	fill := func(s string) string {
		for k, v := range vars {
			s = strings.ReplaceAll(s, k, v)
		}
		return s
	}
	var ts []text
	for _, f := range c.Files {
		// "a|b": the first of them there (Homebrew's Go keeps LICENSE above GOROOT).
		var path string
		var b []byte
		var err error
		for _, alt := range strings.Split(f, "|") {
			path = filepath.Clean(fill(alt))
			if b, err = os.ReadFile(path); err == nil {
				break
			}
		}
		if err != nil {
			return acknowledgements.Component{}, fmt.Errorf("%s: %w (fix its path in third_party/acknowledgements.json, or run `just deps` / `just browser` to fetch it)", c.Name, err)
		}
		ts = append(ts, text{label: filepath.Base(path), body: clean(string(b))})
	}
	license := c.License
	if license == "" {
		license = classify(ts)
	}
	return acknowledgements.Component{
		Name: c.Name, Version: fill(c.Version), URL: c.URL, License: license, Apps: sortedApps(c.Apps),
		Note: c.Note, Credits: c.Credits, Text: joinTexts(ts, false),
	}, nil
}

// noticesComponents reads THIRD_PARTY_NOTICES.md: its table (name, link,
// licence, the apps it ships in) and each project's section (its notice).
func noticesComponents(path string) ([]acknowledgements.Component, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := string(b)
	row := regexp.MustCompile(`(?m)^\| \[([^\]]+)\]\(([^)]+)\) \| ([^|]+) \| ([^|]+) \| ([^|]+) \|$`)
	sections := map[string]string{}
	for _, sec := range strings.Split(s, "\n## ")[1:] {
		title, body, _ := strings.Cut(sec, "\n")
		body, _, _ = strings.Cut(body, "\n---")
		if i := strings.Index(body, "```"); i >= 0 {
			body = body[i+3:]
			body = body[strings.Index(body, "\n")+1:]
			body, _, _ = strings.Cut(body, "```")
		}
		sections[strings.TrimSpace(title)] = clean(body)
	}
	var comps []acknowledgements.Component
	for _, m := range row.FindAllStringSubmatch(s, -1) {
		name, url, usedFor, license, shipsIn := m[1], m[2], strings.TrimSpace(m[3]), strings.TrimSpace(m[4]), m[5]
		body := ""
		for title, t := range sections {
			if title == name || strings.HasPrefix(title, name+" ") {
				body = t
			}
		}
		if body == "" {
			return nil, fmt.Errorf("THIRD_PARTY_NOTICES.md: no section for %s", name)
		}
		var apps []string
		for _, a := range strings.Split(shipsIn, ",") {
			switch strings.TrimSpace(a) {
			case "Mac":
				apps = append(apps, "mac")
			case "iPhone":
				apps = append(apps, "ios")
			case "Linux":
				apps = append(apps, "linux")
			default:
				return nil, fmt.Errorf("THIRD_PARTY_NOTICES.md: %s ships in %q (Mac, iPhone or Linux)", name, a)
			}
		}
		// The note says what of it Hammock has, without the repo's paths.
		note := regexp.MustCompile(" ?\\([^)]*`[^)]*\\)").ReplaceAllString(usedFor, "")
		note = strings.ReplaceAll(note, "`", "")
		comps = append(comps, acknowledgements.Component{
			Name: name, URL: url, License: spdx(license), Apps: sortedApps(apps), Note: note + ".", Text: body,
		})
	}
	return comps, nil
}

func spdx(l string) string {
	switch l {
	case "CC0 1.0":
		return "CC0-1.0"
	}
	return l
}

// MARK: Licence files

var licenseName = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents)([-._].*)?$`)

// licenseFiles reads a package's licence files (LICENSE*, COPYING*,
// NOTICE*, PATENTS) at its root, leaving out those named in skip.
func licenseFiles(dir string, skip []string) ([]text, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ts []text
	for _, e := range entries {
		if e.IsDir() || !licenseName.MatchString(e.Name()) || slices.Contains(skip, e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		ts = append(ts, text{label: e.Name(), body: clean(string(b))})
	}
	if len(ts) == 0 {
		return nil, fmt.Errorf("no licence file in %s", dir)
	}
	// LICENSE first, then the rest by name.
	sort.SliceStable(ts, func(i, j int) bool {
		a, b := strings.ToUpper(ts[i].label), strings.ToUpper(ts[j].label)
		if (a == "LICENSE") != (b == "LICENSE") {
			return a == "LICENSE"
		}
		return a < b
	})
	return ts, nil
}

func clean(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// classify names the licences in a package's texts, as SPDX ids.
func classify(ts []text) string {
	var ids []string
	add := func(id string) {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	for _, t := range ts {
		b := strings.Join(strings.Fields(t.body), " ")
		switch {
		case strings.Contains(strings.ToUpper(t.label), "PATENTS"):
			// Go's patent grant, beside its BSD licence.
		case strings.Contains(strings.ToUpper(t.label), "3RD-PARTY"):
			// Notices of what a package includes, each with its own licence.
		case strings.Contains(b, "Apache License") && strings.Contains(b, "Version 2.0"):
			add("Apache-2.0")
			if strings.Contains(b, "Permission is hereby granted, free of charge") {
				add("MIT")
			}
		case strings.Contains(b, "Permission is hereby granted, free of charge"):
			add("MIT")
		case strings.Contains(b, "Redistribution and use in source and binary forms"):
			if strings.Contains(b, "Neither the name") || strings.Contains(b, "names of its contributors") || strings.Contains(b, "name of the copyright holder") {
				add("BSD-3-Clause")
			} else {
				add("BSD-2-Clause")
			}
		case strings.Contains(b, "Permission to use, copy, modify, and/or distribute") || strings.Contains(b, "Permission to use, copy, modify, and distribute"):
			add("ISC")
		case strings.Contains(b, "The author disclaims copyright to this source code"):
			add("blessing")
		case strings.Contains(b, "dedicated to the public domain"):
			add("Public-Domain")
		case strings.Contains(b, "CC0 1.0 Universal"):
			add("CC0-1.0")
		case strings.Contains(strings.ToLower(t.label), "logo"):
			// An image's credit (modernc.org/memory's logo), not code.
		default:
			add("LicenseRef-see-text")
		}
	}
	return strings.Join(ids, " AND ")
}

// MARK: Helpers

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func output(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return string(out), nil
}
