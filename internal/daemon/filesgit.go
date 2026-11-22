package daemon

import (
	"context"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// gitStatus is a repository's working tree as git status sees it, for a
// files pane's colours: each changed file's status, and each folder's (the
// strongest inside it), by path relative to the repository.
type gitStatus struct {
	codes     map[string]string // files and folders
	untracked []string          // folders untracked as a whole ("x", for "x/")
}

// gitRank orders statuses for a folder's: the one that most needs your
// attention wins.
var gitRank = map[string]int{"": 0, "U": 1, "R": 2, "A": 3, "D": 4, "M": 5, "C": 6}

// readGitStatus asks git (without taking its index lock: a pane looking
// mustn't get in the way of a commit). A repository git can't read is
// shown without colours.
func readGitStatus(repo string) *gitStatus {
	g := &gitStatus{codes: map[string]string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-C", repo, "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignore-submodules=dirty")
	out, err := cmd.Output()
	if err != nil {
		return g
	}
	g.parse(string(out))
	return g
}

func (g *gitStatus) parse(out string) {
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		x, y, p := f[0], f[1], f[3:]
		if x == 'R' || x == 'C' {
			i++ // the name it had: the next field
		}
		code := gitCode(x, y)
		if code == "U" && strings.HasSuffix(p, "/") {
			p = strings.TrimSuffix(p, "/")
			g.untracked = append(g.untracked, p)
		}
		g.mark(p, code)
	}
}

func gitCode(x, y byte) string {
	switch {
	case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
		return "C"
	case x == '?':
		return "U"
	case x == 'R':
		return "R"
	case x == 'A':
		return "A"
	case x == 'D' || y == 'D':
		return "D"
	}
	return "M"
}

// mark records p's code, and its folders'.
func (g *gitStatus) mark(p, code string) {
	g.codes[p] = code
	for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if gitRank[code] <= gitRank[g.codes[dir]] {
			break // it and the folders above it have as strong a one
		}
		g.codes[dir] = code
	}
}

// status is the status of rel (a file, or a folder when dir).
func (g *gitStatus) status(rel string, dir bool) string {
	if c, ok := g.codes[rel]; ok {
		return c
	}
	for _, u := range g.untracked {
		if strings.HasPrefix(rel, u+"/") {
			return "U"
		}
	}
	return ""
}

// sameIn reports whether folder dir's entries have the same statuses in g
// and o.
func (g *gitStatus) sameIn(o *gitStatus, repo, dir string) bool {
	rel, err := filepath.Rel(repo, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return true
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		rel = ""
	}
	in := func(p string) bool {
		if rel == "" {
			return !strings.Contains(p, "/")
		}
		return strings.HasPrefix(p, rel+"/") && !strings.Contains(p[len(rel)+1:], "/")
	}
	for p, c := range g.codes {
		if in(p) && o.codes[p] != c {
			return false
		}
	}
	for p, c := range o.codes {
		if in(p) && g.codes[p] != c {
			return false
		}
	}
	// A folder untracked as a whole, at or above this one.
	covers := func(s *gitStatus) bool {
		for _, u := range s.untracked {
			if rel == u || strings.HasPrefix(rel, u+"/") {
				return true
			}
		}
		return false
	}
	return covers(g) == covers(o)
}
