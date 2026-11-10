package agents

import (
	"path/filepath"
	"strings"

	"github.com/homegrownsw/hammock/internal/agentview"
)

// Kilo is Kilo Code's CLI, a fork of OpenCode 1.x: the same flags, plugins
// and session database (in V1's schema), under its own names. Facts from
// its source, Kilo-Org/kilocode packages/opencode (@kilocode/cli 7.8.27).
var Kilo = &Agent{
	ID: "kilo", Order: 90, Name: "Kilo CLI", Short: "Kilo", Command: "kilo",
	// npm's wrapper (bin/kilo, also linked as kilocode) runs the native
	// binary, or a cached copy of it named .kilo.
	Procs:       []string{"kilocode", ".kilo"},
	Install:     "npm install -g @kilocode/cli",
	LiveUsage:   kiloLiveUsage,
	Hooks:       true,
	Start:       openCodeStart("kilo"),
	Resume:      func(id string) string { return "kilo --session " + Quote(id) },
	ResumeID:    openCodeResumeID,
	Schedulable: true,
	Screen:      openCodeScreen,

	// $XDG_DATA_HOME/kilo, with kilo.db (or kilo-<channel>.db) in it;
	// KILO_DB names another.
	Roots:    kiloRoots,
	RootEnv:  map[string]string{"XDG_DATA_HOME": "kilo"},
	Locate:   func(id string) (string, bool) { return kiloStore.locate(kiloRoots(), id) },
	Newest:   func(cwd string) (string, bool) { return kiloStore.newest(kiloRoots(), cwd) },
	Sessions: kiloStore.sessions,
	Index:    openCodeIndex,
	OwnsFile: kiloStore.owns,
	Stat:     openCodeStat,

	NewParser: func(cwd string, _ bool) agentview.Parser { return agentview.NewOpenCode(cwd) },
	Whole:     true,
	Open:      openCodeOpenSession,
	// Its usage (Kilo's gateway credits) comes only from Kilo's server.
}

// kiloStore is Kilo's databases: kilo.db, a channel build's kilo-<channel>.db
// and KILO_DB's (not its session-export.db, a cache for uploading them).
var kiloStore = openCodeDBStore{dbName: func(name string) bool {
	if db := envOrEmpty("KILO_DB"); filepath.IsAbs(db) && name == filepath.Base(db) {
		return true
	}
	return name == "kilo.db" || strings.HasPrefix(name, "kilo-") && strings.HasSuffix(name, ".db")
}}

func kiloRoots() []string {
	dirs := []string{filepath.Join(xdg("XDG_DATA_HOME", ".local", "share"), "kilo")}
	if db := envOrEmpty("KILO_DB"); filepath.IsAbs(db) {
		dirs = append(dirs, filepath.Dir(db))
	}
	return dirs
}

func init() { Register(Kilo) }
