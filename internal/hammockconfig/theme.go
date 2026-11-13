package hammockconfig

import (
	"fmt"
	"strings"
	"unicode"
)

// AppThemes are the curated themes' names, in Settings' order: keep them
// as apple/Sources/HammockKit/AppTheme.swift has them (AppTheme.all), which
// holds their colours.
var AppThemes = []string{
	"Hammock", "Native", "Graphite", "Ocean", "Lagoon", "Rose", "Lavender", "Sand", "Apricot", "Cocoa",
	"Catppuccin", "Tokyo Night", "Gruvbox", "Nord", "Rosé Pine", "Solarized",
}

// CuratedTheme is what switching to Custom needs to know of a curated
// theme: the settings it's made of.
type CuratedTheme struct {
	Name                    string
	LightTerminal           string
	DarkTerminal            string
	OwnTint                 bool   // one of Hammock's tints (its window theme has its name), not a well-known terminal theme's
	AccentLight, AccentDark uint32 // 0xRRGGBB; both 0: none (Native: the system accent)
}

// Curated are the curated themes, copied from
// apple/Sources/HammockKit/AppTheme.swift (AppTheme.all: names, terminal
// names, ownTint and accent): keep them the same.
var Curated = []CuratedTheme{
	{"Hammock", BuiltInLight, BuiltInDark, true, 0x1F7A57, 0x3FB88A},
	{"Native", BuiltInLight, BuiltInDark, true, 0, 0},
	{"Graphite", "Graphite Light", "Graphite Dark", true, 0x3E3E46, 0xE2E2E8},
	{"Ocean", "Ocean Light", "Ocean Dark", true, 0x2B6CC4, 0x72A8F0},
	{"Lagoon", BuiltInLight, BuiltInDark, true, 0x0F828A, 0x3BCECE},
	{"Rose", "Rose Light", "Rose Dark", true, 0xCC4A72, 0xEA7398},
	{"Lavender", "Lavender Light", "Lavender Dark", true, 0x7352D1, 0xA68EF2},
	{"Sand", "Sand Light", "Sand Dark", true, 0xA87A1F, 0xD9A743},
	{"Apricot", BuiltInLight, BuiltInDark, true, 0xCD641D, 0xF19E55},
	{"Cocoa", BuiltInLight, BuiltInDark, true, 0x7D553B, 0xC9A487},
	{"Catppuccin", "Catppuccin Latte", "Catppuccin Mocha", false, 0x8839EF, 0xCBA6F7},
	{"Tokyo Night", "TokyoNight Day", "TokyoNight", false, 0x2E7DE9, 0x7AA2F7},
	{"Gruvbox", "Gruvbox Light", "Gruvbox Dark", false, 0xAF3A03, 0xFE8019},
	{"Nord", "Nord Light", "Nord", false, 0x5E81AC, 0x88C0D0},
	{"Rosé Pine", "Rose Pine Dawn", "Rose Pine", false, 0x907AA9, 0xC4A7E7},
	{"Solarized", "iTerm2 Solarized Light", "iTerm2 Solarized Dark", false, 0x268BD2, 0x268BD2},
}

const (
	// DefaultTheme is a new install's theme.
	DefaultTheme = "Hammock"
	// NativeTheme takes the system's colours.
	NativeTheme = "Native"
	// CustomTheme is `app-theme = custom`: the separate settings
	// (CustomKeys) apply.
	CustomTheme = "custom"
	// MatchTerminal is the window theme made from the terminal's backgrounds.
	MatchTerminal = "Match Terminal"
)

// The terminal's built-in themes (TerminalTheme.builtInLight and Dark).
const (
	BuiltInLight = "Hammock Light"
	BuiltInDark  = "Hammock Dark"
)

// CustomKeys are the settings a Custom theme is made of. Someone who set
// any of them before app-theme existed keeps their look (custom) until they
// pick a theme (AppConfig.customKeys).
var CustomKeys = []string{"theme", "window-theme", "agent-color", "active-pane-border-color", "sidebar-color", "toolbar-color"}

// foldName is a theme name for comparing: lower case, accents dropped
// (precomposed or combining, so NFC and NFD spellings match), trimmed.
func foldName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if unicode.Is(unicode.Mn, r) {
			continue // a combining accent (NFD)
		}
		if f, ok := accentFold[r]; ok {
			r = f
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// accentFold takes Latin letters' accents off (what theme names might use).
var accentFold = func() map[rune]rune {
	m := map[rune]rune{}
	for base, accented := range map[rune]string{
		'a': "àáâãäåā", 'c': "çćč", 'e': "èéêëēėę", 'i': "ìíîïī", 'n': "ñń", 'o': "òóôõöøō",
		'u': "ùúûüū", 'y': "ýÿ", 's': "śš", 'z': "źżž",
		'A': "ÀÁÂÃÄÅĀ", 'C': "ÇĆČ", 'E': "ÈÉÊËĒĖĘ", 'I': "ÌÍÎÏĪ", 'N': "ÑŃ", 'O': "ÒÓÔÕÖØŌ",
		'U': "ÙÚÛÜŪ", 'Y': "ÝŸ", 'S': "ŚŠ", 'Z': "ŹŻŽ",
	} {
		for _, r := range accented {
			m[r] = base
		}
	}
	return m
}()

// ThemeName is name as a theme's own (any case, with or without accents),
// or CustomTheme, and whether it's one.
func ThemeName(name string) (string, bool) {
	n := foldName(name)
	if n == CustomTheme {
		return CustomTheme, true
	}
	for _, t := range AppThemes {
		if foldName(t) == n {
			return t, true
		}
	}
	return "", false
}

// CuratedNamed is the curated theme of that name (as ThemeName matches).
func CuratedNamed(name string) (CuratedTheme, bool) {
	if n, ok := ThemeName(name); ok {
		for _, t := range Curated {
			if t.Name == n {
				return t, true
			}
		}
	}
	return CuratedTheme{}, false
}

// AppTheme is the theme in use, from the config's values (Values): app-theme,
// or with none (or empty), custom if a Custom setting is there, else the
// default. An unknown name is the default too.
func AppTheme(values map[string]string) string {
	v := strings.TrimSpace(values["app-theme"])
	if v == "" {
		if HasCustomValues(values) {
			return CustomTheme
		}
		return DefaultTheme
	}
	if n, ok := ThemeName(v); ok {
		return n
	}
	return DefaultTheme
}

// HasCustomValues is whether any of Custom's settings is set (not empty).
func HasCustomValues(values map[string]string) bool {
	for _, k := range CustomKeys {
		if strings.TrimSpace(values[k]) != "" {
			return true
		}
	}
	return false
}

// CustomChanges are what switching to Custom writes, as the Mac app's
// Settings do (AppConfig.setAppTheme): app-theme = custom, and when none of
// Custom's settings is in the file yet, each one seeded from the theme in
// use (values: the config's), so nothing changes until you change it. Any
// set, they're the Custom you had before, and stay.
func CustomChanges(values map[string]string) []Change {
	out := []Change{{"app-theme", CustomTheme}}
	if HasCustomValues(values) {
		return out
	}
	t, ok := CuratedNamed(AppTheme(values))
	if !ok {
		return out
	}
	native := t.Name == NativeTheme
	window := MatchTerminal
	if native {
		window = ""
	} else if t.OwnTint {
		window = t.Name
	}
	term := ""
	if t.LightTerminal != BuiltInLight || t.DarkTerminal != BuiltInDark {
		term = "dark:" + t.DarkTerminal + ",light:" + t.LightTerminal
	}
	agent, border := "", ""
	if !native {
		agent = t.Name
		border = FormatPair(t.AccentLight, t.AccentDark)
	}
	return append(out,
		Change{"window-theme", window}, Change{"theme", term}, Change{"agent-color", agent},
		Change{"active-pane-border-color", border}, Change{"sidebar-color", ""}, Change{"toolbar-color", ""})
}

// FormatPair is a colour as the config writes it: `#RRGGBB`, or
// `light:#RRGGBB,dark:#RRGGBB` when they differ (AppTheme.format).
func FormatPair(light, dark uint32) string {
	if light == dark {
		return fmt.Sprintf("#%06X", light)
	}
	return fmt.Sprintf("light:#%06X,dark:#%06X", light, dark)
}

// TerminalThemes is a `theme` value's light and dark names: `dark:A,light:B`
// (either order) or one name for both; the built-ins for what's missing.
func TerminalThemes(value string) (light, dark string) {
	light, dark = BuiltInLight, BuiltInDark
	for _, part := range strings.Split(value, ",") {
		p := strings.TrimSpace(part)
		switch {
		case p == "":
		case strings.HasPrefix(p, "light:"):
			light = strings.TrimSpace(p[len("light:"):])
		case strings.HasPrefix(p, "dark:"):
			dark = strings.TrimSpace(p[len("dark:"):])
		default:
			light, dark = p, p
		}
	}
	return light, dark
}
