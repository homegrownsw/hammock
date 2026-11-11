// Package gitdiff works out the diffs diff panes show: what a source
// (uncommitted changes, a branch, a commit, a range) compares, the files that
// differ, each file's two sides, and a fingerprint that changes when any of
// that would. It runs git; nothing here writes to the repository.
package gitdiff

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// EmptyTree is git's empty tree: the base of a root commit, or of a
// repository with no commits yet.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// MaxSide is the most of one side of a file sent to be shown: past it, the
// file says it's too large (with its sizes) rather than a client loading
// something that would make it crawl.
const MaxSide = 8 << 20

// Resolved is a source pinned to commits: what's compared, from where to where.
type Resolved struct {
	Repo   string // the repository's top folder
	Source *pb.DiffSource
	Title  string
	Base   string // the commit (or EmptyTree) compared from
	Head   string // compared to; "" the working tree
	Branch string // HEAD's branch, "" detached
	// BaseRef is the ref a branch diff is against (origin/main), for its
	// fingerprint: the merge base moves when it does.
	BaseRef string
}

// Worktree reports whether the diff's new side is the working tree.
func (r *Resolved) Worktree() bool { return r.Head == "" }

// Live reports whether the diff can change without its source changing:
// the working tree, or a branch (HEAD and its base move).
func (r *Resolved) Live() bool {
	k := r.Source.GetKind()
	return k == pb.DiffSource_KIND_UNCOMMITTED || k == pb.DiffSource_KIND_BRANCH
}

// git runs git in dir and returns its output.
func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	full := append([]string{"-C", dir, "-c", "core.quotepath=off", "-c", "color.ui=false", "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	// Reading never takes the index lock (git status refreshes it otherwise,
	// and an agent's git add would then fail).
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		msg = strings.TrimPrefix(msg, "fatal: ")
		if msg == "" {
			msg = err.Error()
		}
		return out, errors.New(msg)
	}
	return out, nil
}

func line(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := git(ctx, dir, args...)
	return strings.TrimSpace(string(out)), err
}

// TopLevel is the top folder of the repository dir is in.
func TopLevel(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		return "", errors.New("no folder to look for a repository in")
	}
	top, err := line(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return "", fmt.Errorf("%s isn't in a git repository", dir)
		}
		return "", err
	}
	if r, err := filepath.EvalSymlinks(top); err == nil {
		top = r
	}
	return top, nil
}

// head is HEAD's commit ("" before the first commit).
func head(ctx context.Context, repo string) string {
	h, err := line(ctx, repo, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil {
		return ""
	}
	return h
}

// CurrentBranch is HEAD's branch ("" detached).
func CurrentBranch(ctx context.Context, repo string) string {
	b, err := line(ctx, repo, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return b
}

func refExists(ctx context.Context, repo, ref string) bool {
	_, err := git(ctx, repo, "rev-parse", "--verify", "-q", ref+"^{commit}")
	return err == nil
}

// DefaultBranch is the branch pull requests go to: origin's HEAD, else
// main, else master ("" if none of them is there).
func DefaultBranch(ctx context.Context, repo string) string {
	if r, err := line(ctx, repo, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil && r != "" {
		return strings.TrimPrefix(r, "origin/")
	}
	for _, b := range []string{"main", "master"} {
		if refExists(ctx, repo, "refs/heads/"+b) || refExists(ctx, repo, "refs/remotes/origin/"+b) {
			return b
		}
	}
	return ""
}

// baseRef is what a branch diff against base compares with: origin's copy
// of it when there is one (where a pull request would go), else the local
// branch, else base as it's given (a tag, a commit).
func baseRef(ctx context.Context, repo, base string) string {
	if !strings.Contains(base, "/") && refExists(ctx, repo, "refs/remotes/origin/"+base) {
		return "origin/" + base
	}
	return base
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// Resolve pins src (KIND_UNSPECIFIED: the branch against its base when HEAD
// is on a branch that isn't the default one, else what's uncommitted) to
// commits in the repository dir is in.
func Resolve(ctx context.Context, dir string, src *pb.DiffSource) (*Resolved, error) {
	repo, err := TopLevel(ctx, dir)
	if err != nil {
		return nil, err
	}
	s := &pb.DiffSource{}
	if src != nil {
		s = &pb.DiffSource{Kind: src.Kind, Base: strings.TrimSpace(src.Base), Rev: strings.TrimSpace(src.Rev),
			WithUncommitted: src.WithUncommitted, MergeBase: src.MergeBase}
	}
	r := &Resolved{Repo: repo, Source: s, Branch: CurrentBranch(ctx, repo)}
	h := head(ctx, repo)
	if s.Kind == pb.DiffSource_KIND_UNSPECIFIED {
		def := DefaultBranch(ctx, repo)
		if r.Branch != "" && def != "" && r.Branch != def && h != "" && refExists(ctx, repo, baseRef(ctx, repo, def)) {
			s.Kind, s.Base, s.WithUncommitted = pb.DiffSource_KIND_BRANCH, def, true
		} else {
			s.Kind = pb.DiffSource_KIND_UNCOMMITTED
		}
	}
	switch s.Kind {
	case pb.DiffSource_KIND_UNCOMMITTED:
		r.Base = h
		if r.Base == "" {
			r.Base = EmptyTree
		}
		r.Title = "Uncommitted changes"
	case pb.DiffSource_KIND_BRANCH:
		if h == "" {
			return nil, errors.New("there are no commits yet")
		}
		if s.Base == "" {
			s.Base = DefaultBranch(ctx, repo)
			if s.Base == "" {
				return nil, errors.New("no main or master branch to compare with: name one")
			}
		}
		r.BaseRef = baseRef(ctx, repo, s.Base)
		mb, err := line(ctx, repo, "merge-base", r.BaseRef, "HEAD")
		if err != nil || mb == "" {
			if !refExists(ctx, repo, r.BaseRef) {
				return nil, gone("no branch %s to compare with", s.Base)
			}
			return nil, gone("HEAD and %s have no history in common", s.Base)
		}
		r.Base = mb
		if !s.WithUncommitted {
			r.Head = h
		}
		name := r.Branch
		if name == "" {
			name = short(h)
		}
		r.Title = name + " vs " + s.Base
		if s.WithUncommitted {
			r.Title += ", with uncommitted changes"
		}
	case pb.DiffSource_KIND_COMMIT:
		if s.Rev == "" {
			s.Rev = "HEAD"
		}
		c, err := line(ctx, repo, "rev-parse", "--verify", "-q", s.Rev+"^{commit}")
		if err != nil || c == "" {
			return nil, gone("no commit %s", s.Rev)
		}
		r.Head = c
		if p, err := line(ctx, repo, "rev-parse", "--verify", "-q", c+"^1"); err == nil && p != "" {
			r.Base = p
		} else {
			r.Base = EmptyTree
		}
		subject, _ := line(ctx, repo, "log", "-1", "--format=%s", c)
		r.Title = strings.TrimSpace(short(c) + " " + subject)
		s.Rev = c
	case pb.DiffSource_KIND_RANGE:
		if s.Base == "" || s.Rev == "" {
			return nil, errors.New("a range needs where it starts and where it ends")
		}
		from, err := line(ctx, repo, "rev-parse", "--verify", "-q", s.Base+"^{commit}")
		if err != nil || from == "" {
			return nil, gone("no commit %s", s.Base)
		}
		to, err := line(ctx, repo, "rev-parse", "--verify", "-q", s.Rev+"^{commit}")
		if err != nil || to == "" {
			return nil, gone("no commit %s", s.Rev)
		}
		dots := ".."
		if s.MergeBase {
			mb, err := line(ctx, repo, "merge-base", from, to)
			if err != nil || mb == "" {
				return nil, gone("%s and %s have no history in common", s.Base, s.Rev)
			}
			from, dots = mb, "..."
		}
		r.Title = name(s.Base) + dots + name(s.Rev)
		r.Base, r.Head = from, to
	default:
		return nil, fmt.Errorf("unknown kind of diff %v", s.Kind)
	}
	return r, nil
}

// GoneError says what a diff compares isn't there any more (a commit, a
// branch): unlike a folder that isn't there (a disk not mounted yet) or git
// taking too long, it won't come back.
type GoneError struct{ msg string }

func (e *GoneError) Error() string { return e.msg }

func gone(format string, a ...any) error { return &GoneError{fmt.Sprintf(format, a...)} }

// IsGone reports whether err is a GoneError.
func IsGone(err error) bool {
	var g *GoneError
	return errors.As(err, &g)
}

// name shortens a commit's id for a title; a ref's name stays.
func name(rev string) string {
	if len(rev) >= 12 && strings.Trim(rev, "0123456789abcdef") == "" {
		return short(rev)
	}
	return rev
}

// ParseRev reads a commit or a range as git writes them (a..b, a...b) into a
// source. "" is KIND_UNSPECIFIED.
func ParseRev(s string) *pb.DiffSource {
	s = strings.TrimSpace(s)
	if s == "" {
		return &pb.DiffSource{}
	}
	if a, b, ok := strings.Cut(s, "..."); ok {
		return &pb.DiffSource{Kind: pb.DiffSource_KIND_RANGE, Base: orHead(a), Rev: orHead(b), MergeBase: true}
	}
	if a, b, ok := strings.Cut(s, ".."); ok {
		return &pb.DiffSource{Kind: pb.DiffSource_KIND_RANGE, Base: orHead(a), Rev: orHead(b)}
	}
	return &pb.DiffSource{Kind: pb.DiffSource_KIND_COMMIT, Rev: s}
}

func orHead(s string) string {
	if s == "" {
		return "HEAD"
	}
	return s
}

// diffArgs are the revisions git diff compares.
func (r *Resolved) diffArgs() []string {
	if r.Head == "" {
		return []string{r.Base}
	}
	return []string{r.Base, r.Head}
}

// Files lists what differs, sorted by path as git gives them, untracked
// files (for a working-tree diff) after.
func Files(ctx context.Context, r *Resolved) ([]*pb.DiffFile, error) {
	common := []string{"diff", "-z", "-M", "--no-ext-diff", "--no-textconv", "--ignore-submodules=dirty"}
	st, err := git(ctx, r.Repo, append(append(append([]string{}, common...), "--name-status"), r.diffArgs()...)...)
	if err != nil {
		return nil, err
	}
	ns, err := git(ctx, r.Repo, append(append(append([]string{}, common...), "--numstat"), r.diffArgs()...)...)
	if err != nil {
		return nil, err
	}
	files := parseNameStatus(st)
	counts := parseNumstat(ns)
	for _, f := range files {
		if c, ok := counts[f.Path]; ok {
			f.Added, f.Removed, f.Binary = c.added, c.removed, c.binary
		}
	}
	if r.Worktree() {
		out, err := git(ctx, r.Repo, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		for _, p := range splitZ(out) {
			f := &pb.DiffFile{Path: p, Status: pb.DiffFile_STATUS_ADDED, Untracked: true}
			full := filepath.Join(r.Repo, filepath.FromSlash(p))
			st, err := os.Lstat(full)
			if err != nil {
				continue
			}
			switch {
			case st.Mode()&os.ModeSymlink != 0:
				f.Added = 1 // git keeps a link as the path it points to: one line
			case !st.Mode().IsRegular():
				continue // a nested repository, a socket
			case st.Size() > MaxSide:
				// Too big to show: not read (here, on every refresh) to count.
			default:
				if b, n, err := countLines(full); err == nil {
					f.Binary, f.Added = b, n
				}
			}
			files = append(files, f)
		}
	}
	markGenerated(ctx, r.Repo, files)
	return files, nil
}

type numstat struct {
	added, removed uint32
	binary         bool
}

// parseNumstat reads git diff --numstat -z: "A\tR\tpath\0", or for a rename
// "A\tR\t\0old\0new\0". Binary files count "-".
func parseNumstat(out []byte) map[string]numstat {
	m := map[string]numstat{}
	parts := splitZ(out)
	for i := 0; i < len(parts); i++ {
		fields := strings.SplitN(parts[i], "\t", 3)
		if len(fields) < 3 {
			continue
		}
		var c numstat
		if fields[0] == "-" {
			c.binary = true
		} else {
			a, _ := strconv.ParseUint(fields[0], 10, 32)
			d, _ := strconv.ParseUint(fields[1], 10, 32)
			c.added, c.removed = uint32(a), uint32(d)
		}
		p := fields[2]
		if p == "" && i+2 < len(parts) {
			p = parts[i+2] // the new path
			i += 2
		}
		m[p] = c
	}
	return m
}

// parseNameStatus reads git diff --name-status -z: "M\0path\0", or for a
// rename or copy "R100\0old\0new\0".
func parseNameStatus(out []byte) []*pb.DiffFile {
	var files []*pb.DiffFile
	parts := splitZ(out)
	for i := 0; i < len(parts); i++ {
		code := parts[i]
		if code == "" || i+1 >= len(parts) {
			continue
		}
		f := &pb.DiffFile{}
		switch code[0] {
		case 'A':
			f.Status = pb.DiffFile_STATUS_ADDED
		case 'D':
			f.Status = pb.DiffFile_STATUS_DELETED
		case 'T':
			f.Status = pb.DiffFile_STATUS_TYPE_CHANGED
		case 'R', 'C':
			f.Status = pb.DiffFile_STATUS_RENAMED
			if code[0] == 'C' {
				f.Status = pb.DiffFile_STATUS_COPIED
			}
			if i+2 >= len(parts) {
				i = len(parts)
				continue
			}
			f.OldPath, f.Path = parts[i+1], parts[i+2]
			files = append(files, f)
			i += 2
			continue
		default:
			f.Status = pb.DiffFile_STATUS_MODIFIED
		}
		f.Path = parts[i+1]
		files = append(files, f)
		i++
	}
	return files
}

func splitZ(out []byte) []string {
	s := strings.TrimSuffix(string(out), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// countLines reads a file not yet in git: whether it looks binary (a NUL
// early on, as git decides) and how many lines it has.
func countLines(p string) (binary bool, n uint32, err error) {
	f, err := os.Open(p)
	if err != nil {
		return false, 0, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64<<10)
	head, _ := br.Peek(8000)
	if bytes.IndexByte(head, 0) >= 0 {
		return true, 0, nil
	}
	buf := make([]byte, 64<<10)
	var last byte
	var total int64
	for {
		k, err := br.Read(buf)
		if k > 0 {
			n += uint32(bytes.Count(buf[:k], []byte{'\n'}))
			last = buf[k-1]
			total += int64(k)
		}
		if err != nil {
			break
		}
	}
	if total > 0 && last != '\n' {
		n++
	}
	return false, n, nil
}

// generatedNames are files that are written by tools, not people: they
// start collapsed.
var generatedNames = map[string]bool{
	"package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "bun.lockb": true,
	"Cargo.lock": true, "go.sum": true, "Gemfile.lock": true, "Podfile.lock": true, "composer.lock": true,
	"poetry.lock": true, "Pipfile.lock": true, "uv.lock": true, "Package.resolved": true, "flake.lock": true,
	"mix.lock": true, "pubspec.lock": true, "packages.lock.json": true,
}

var generatedSuffixes = []string{".pb.go", ".pb.swift", "_pb2.py", "_pb2.pyi", ".pb.cc", ".pb.h", ".min.js", ".min.css", ".js.map", ".css.map", ".snap"}

// markGenerated sets generated on lockfiles and the like, and files
// .gitattributes marks linguist-generated.
func markGenerated(ctx context.Context, repo string, files []*pb.DiffFile) {
	var paths []string
	for _, f := range files {
		base := path.Base(f.Path)
		if generatedNames[base] {
			f.Generated = true
			continue
		}
		for _, s := range generatedSuffixes {
			if strings.HasSuffix(base, s) {
				f.Generated = true
				break
			}
		}
		if !f.Generated {
			paths = append(paths, f.Path)
		}
	}
	if len(paths) == 0 {
		return
	}
	cmd := []string{"check-attr", "-z", "linguist-generated", "--"}
	set := map[string]bool{}
	// In batches: a command line has a limit.
	for len(paths) > 0 {
		n := min(len(paths), 500)
		out, err := git(ctx, repo, append(cmd, paths[:n]...)...)
		paths = paths[n:]
		if err != nil {
			return
		}
		// path\0attr\0value\0
		parts := splitZ(out)
		for i := 0; i+2 < len(parts); i += 3 {
			if v := parts[i+2]; v == "set" || v == "true" {
				set[parts[i]] = true
			}
		}
	}
	for _, f := range files {
		if set[f.Path] {
			f.Generated = true
		}
	}
}

// Totals adds up files' lines.
func Totals(files []*pb.DiffFile) (added, removed uint32) {
	for _, f := range files {
		added += f.Added
		removed += f.Removed
	}
	return
}

// imageTypes are the binary files shown as pictures.
var imageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".bmp": "image/bmp", ".ico": "image/x-icon", ".heic": "image/heic", ".tif": "image/tiff", ".tiff": "image/tiff",
}

// FileText reads f's two sides.
func FileText(ctx context.Context, r *Resolved, f *pb.DiffFile) (*pb.DiffFileText, error) {
	t := &pb.DiffFileText{Path: f.Path}
	mime := imageTypes[strings.ToLower(path.Ext(f.Path))]
	if f.Binary {
		t.Mime = mime
	}
	oldPath := f.Path
	if f.OldPath != "" {
		oldPath = f.OldPath
	}
	var oldB, newB []byte
	var err error
	if f.Status != pb.DiffFile_STATUS_ADDED && !f.Untracked {
		if oldB, t.OldSize, err = blob(ctx, r.Repo, r.Base, oldPath); err != nil {
			return nil, err
		}
	}
	if f.Status != pb.DiffFile_STATUS_DELETED {
		if r.Worktree() {
			newB, t.NewSize, err = worktreeFile(filepath.Join(r.Repo, filepath.FromSlash(f.Path)))
		} else {
			newB, t.NewSize, err = blob(ctx, r.Repo, r.Head, f.Path)
		}
		if err != nil {
			return nil, err
		}
	}
	if t.OldSize > MaxSide || t.NewSize > MaxSide {
		t.TooLarge = true
		return t, nil
	}
	if f.Binary {
		if mime != "" {
			t.OldData, t.NewData = oldB, newB
		}
		return t, nil
	}
	t.OldText, t.NewText = text(oldB), text(newB)
	return t, nil
}

// text is b as valid UTF-8 (a file in another encoding shows its odd
// bytes as replacement characters, as any editor would).
func text(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "�")
}

// blob reads path as rev has it: its bytes, unless it's over MaxSide, and
// its size either way.
func blob(ctx context.Context, repo, rev, p string) ([]byte, uint64, error) {
	if rev == EmptyTree {
		return nil, 0, nil
	}
	spec := rev + ":" + p
	sz, err := line(ctx, repo, "cat-file", "-s", spec)
	if err != nil {
		return nil, 0, fmt.Errorf("couldn't read %s at %s: %v", p, short(rev), err)
	}
	n, _ := strconv.ParseUint(sz, 10, 64)
	if n > MaxSide {
		return nil, n, nil
	}
	typ, _ := line(ctx, repo, "cat-file", "-t", spec)
	if typ != "blob" {
		return nil, 0, nil // a submodule's commit
	}
	b, err := git(ctx, repo, "cat-file", "blob", spec)
	if err != nil {
		return nil, 0, err
	}
	return b, n, nil
}

func worktreeFile(p string) ([]byte, uint64, error) {
	st, err := os.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil // gone since the list was made
		}
		return nil, 0, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		// git keeps a link as its target's path.
		t, err := os.Readlink(p)
		return []byte(t), uint64(len(t)), err
	}
	if !st.Mode().IsRegular() {
		return nil, 0, nil
	}
	n := uint64(st.Size())
	if n > MaxSide {
		return nil, n, nil
	}
	b, err := os.ReadFile(p)
	return b, uint64(len(b)), err
}

// Fingerprint changes whenever the diff would: HEAD moving, the base
// moving, a file in the working tree changing. Cheap enough to run every
// couple of seconds.
func Fingerprint(ctx context.Context, r *Resolved) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "head %s\n", head(ctx, r.Repo))
	if r.BaseRef != "" {
		b, _ := line(ctx, r.Repo, "rev-parse", "--verify", "-q", r.BaseRef+"^{commit}")
		fmt.Fprintf(h, "base %s\n", b)
	}
	if r.Worktree() {
		out, err := git(ctx, r.Repo, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=dirty")
		if err != nil {
			return "", err
		}
		h.Write(out)
		// Editing a file that's already changed leaves status the same:
		// its size and time don't.
		for _, entry := range splitZ(out) {
			if len(entry) < 4 {
				continue
			}
			p := entry[3:]
			if st, err := os.Lstat(filepath.Join(r.Repo, filepath.FromSlash(p))); err == nil {
				fmt.Fprintf(h, "%s %d %d\n", p, st.Size(), st.ModTime().UnixNano())
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Commits are HEAD's latest commits, newest first.
func Commits(ctx context.Context, repo string, n int) []*pb.DiffCommit {
	out, err := git(ctx, repo, "log", "-n", strconv.Itoa(n), "--format=%H%x00%s%x00%an%x00%ct%x1e", "HEAD")
	if err != nil {
		return nil
	}
	var cs []*pb.DiffCommit
	for _, rec := range strings.Split(string(out), "\x1e") {
		f := strings.Split(strings.TrimSpace(rec), "\x00")
		if len(f) < 4 {
			continue
		}
		t, _ := strconv.ParseInt(f[3], 10, 64)
		cs = append(cs, &pb.DiffCommit{Sha: f[0], Subject: f[1], Author: f[2], Unix: t})
	}
	return cs
}

// Branches are the branches a diff can compare with, def first, then the
// most recently changed.
func Branches(ctx context.Context, repo, def string) []string {
	out, err := git(ctx, repo, "for-each-ref", "--sort=-committerdate", "--count=60", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil
	}
	list := []string{}
	if def != "" {
		list = append(list, def)
	}
	for _, b := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if b != "" && b != def {
			list = append(list, b)
		}
	}
	return list
}
