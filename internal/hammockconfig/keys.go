package hammockconfig

import (
	"fmt"
	"strings"
)

// Key is one setting the config file can hold. Keys is the one list of them:
// the commented header a new config file starts with (Go's, and the Mac
// app's, generated into ConfigHeader.generated.swift by `just config-header`)
// and the docs site's config reference are both made from it, so a new key
// is added here and nowhere else needs its description.
type Key struct {
	Name string
	// Group is the docs' section, in the order Groups lists.
	Group string
	// Value is what the header shows after "=": the choices ("auto | light |
	// dark"), the default, or a placeholder (NAME, #RRGGBB).
	Value string
	// Default is the value when the key is unset, as written in the file;
	// empty when unset means something else, which Doc says.
	Default string
	// Repeat: one line per value (keybind, remote-network, pane-icon).
	Repeat bool
	// Doc says what it does, in a line: shown in the header and the docs.
	Doc string
	// Example, for the docs: a line or two of config to copy.
	Example string
	// Only: where the key applies, when not everywhere ("Mac", "hmk tui").
	Only string
}

// DocsURL is the docs site (site/ in the repo): `hmk docs`, the TUI's
// palette and the config file's header point to it.
const DocsURL = "https://homegrownsw.com/hammock/docs/"

// Groups orders the docs' sections.
var Groups = []string{
	"Appearance", "Terminal", "Window and sidebar", "Panes", "Agents", "Agent view", "Notifications",
	"Keyboard", "Editor panes", "Diff panes", "Browser panes", "Simulator panes", "Tools",
	"Remote access and linked computers", "Usage limits", "TUI",
}

var Keys = []Key{
	// Appearance
	{Name: "appearance", Group: "Appearance", Value: "auto | light | dark", Default: "auto",
		Doc: "light or dark, or follow macOS", Only: "Mac"},
	{Name: "app-theme", Group: "Appearance", Value: "Hammock", Default: "Hammock",
		Doc:     "Hammock, Native, Graphite, Ocean, Lagoon, Rose, Lavender, Sand, Apricot, Cocoa, Catppuccin, Tokyo Night, Gruvbox, Nord, Rosé Pine, Solarized; or custom: the theme, window-theme, agent-color, active-pane-border-color, sidebar-color and toolbar-color keys apply (unset with any of those set: custom)",
		Example: "app-theme = Nord"},
	{Name: "theme", Group: "Appearance", Value: "dark:NAME,light:NAME",
		Doc:     "the terminal's colours, from Ghostty theme files (or `theme = NAME` for both; app-theme = custom); unset: Hammock's own; see Settings ▸ Appearance",
		Example: "app-theme = custom\ntheme = dark:Catppuccin Mocha,light:Catppuccin Latte"},
	{Name: "window-theme", Group: "Appearance", Value: "Native | Graphite | Hammock | Ocean | Lagoon | Rose | Lavender | Sand | Apricot | Cocoa | Match Terminal", Default: "Native",
		Doc: "the window's colours (app-theme = custom)"},
	{Name: "agent-color", Group: "Appearance", Value: "Ocean",
		Doc: "the agent view's colour: a colour or theme name, or #RRGGBB, light:#…,dark:#…; unset: the window theme's (app-theme = custom)"},
	{Name: "sidebar-color", Group: "Appearance", Value: "#RRGGBB",
		Doc: "or light:#…,dark:#…; overrides the window theme (app-theme = custom)", Only: "Mac"},
	{Name: "toolbar-color", Group: "Appearance", Value: "#RRGGBB",
		Doc: "or light:#…,dark:#…; overrides the window theme (app-theme = custom)", Only: "Mac"},
	{Name: "sidebar-opacity", Group: "Appearance", Value: "0 … 1", Default: "0",
		Doc: "0 glassy, 1 opaque", Only: "Mac"},

	// Terminal
	{Name: "font-family", Group: "Terminal", Value: "NAME",
		Doc: "the terminal's font; unset: your Ghostty config's, else SF Mono", Example: "font-family = JetBrains Mono", Only: "Mac"},
	{Name: "font-size", Group: "Terminal", Value: "13",
		Doc: "in points; unset: your Ghostty config's, else 13", Only: "Mac"},
	{Name: "font-thicken", Group: "Terminal", Value: "false", Default: "false",
		Doc: "draw the terminal's text a little heavier; unset: your Ghostty config's", Only: "Mac"},
	{Name: "terminal-renderer", Group: "Terminal", Value: "metal", Default: "metal",
		Doc: "or coretext: CoreGraphics, as before Metal; for terminal panes opened after it's set", Only: "Mac"},
	{Name: "adjust-cell-height", Group: "Terminal", Value: "20%",
		Doc: "terminal line spacing: a percentage of the line height, or points; negative: tighter; unset: your Ghostty config's, else none", Only: "Mac"},
	{Name: "cursor-style", Group: "Terminal", Value: "block | bar | bar_thin | underline | block_hollow", Default: "block",
		Doc: "the terminal's cursor; unset: your Ghostty config's", Only: "Mac"},
	{Name: "cursor-style-blink", Group: "Terminal", Value: "true", Default: "true",
		Doc: "false: a steady cursor; unset: your Ghostty config's", Only: "Mac"},
	{Name: "copy-on-select", Group: "Terminal", Value: "false", Default: "false",
		Doc: "true (or clipboard): copy text as soon as it's selected; your Ghostty config's isn't followed", Only: "Mac"},
	{Name: "clipboard-paste-protection", Group: "Terminal", Value: "true", Default: "true",
		Doc: "ask before a paste that could run commands; unset: your Ghostty config's", Only: "Mac"},
	{Name: "clipboard-paste-bracketed-safe", Group: "Terminal", Value: "true", Default: "true",
		Doc: "no need to ask when the program takes bracketed pastes; unset: your Ghostty config's", Only: "Mac"},
	{Name: "program-icons", Group: "Terminal", Value: "plain | colorful | off", Default: "plain",
		Doc: "a terminal's program: its icon, on a squircle in its colours, or none; agents keep their logo, on a squircle when colorful", Only: "Mac"},
	{Name: "pane-icon", Group: "Terminal", Value: "btop=monitor", Repeat: true,
		Doc:     "a program's icon: a built-in one, like vim, git, python, monitor, or an SF Symbol; several with commas, or repeat the key",
		Example: "pane-icon = btop=monitor\npane-icon = lazygit=git, k9s=shippingbox", Only: "Mac and hmk tui"},

	// Window and sidebar
	{Name: "sidebar-layout", Group: "Window and sidebar", Value: "vertical | compact | horizontal", Default: "vertical",
		Doc: "horizontal: the Group's tabs in a tab bar along the top", Only: "Mac"},
	{Name: "group-headings", Group: "Window and sidebar", Value: "prominent | subtle", Default: "prominent",
		Doc: "subtle: Group headings in the sidebar smaller and dimmer than its tabs", Only: "Mac"},
	{Name: "group-icons", Group: "Window and sidebar", Value: "true", Default: "true",
		Doc: "false: no icons on Group headings in the sidebar", Only: "Mac"},
	{Name: "tab-status", Group: "Window and sidebar", Value: "icon | trailing", Default: "icon",
		Doc: "agent and program status in a sidebar row's icon, or at its end with the agent's logo as the icon", Only: "Mac"},
	{Name: "tab-close-button", Group: "Window and sidebar", Value: "true", Default: "true",
		Doc: "false: no close button on a tab you hover over, in the sidebar or the tab bar", Only: "Mac"},
	{Name: "sidebar-finished", Group: "Window and sidebar", Value: "true", Default: "true",
		Doc: "finished agents get their own list at the bottom of the sidebar", Only: "Mac"},
	{Name: "collapsed-group-agents", Group: "Window and sidebar", Value: "false", Default: "false",
		Doc: "true: a collapsed Group still shows its tabs with an active agent", Only: "Mac"},
	{Name: "keep-empty-groups", Group: "Window and sidebar", Value: "true", Default: "true",
		Doc: "⌘W on a Group's last tab keeps the Group; false: closes it too", Only: "Mac"},
	{Name: "menu-bar", Group: "Window and sidebar", Value: "always | active", Default: "always",
		Doc: "the menu bar item; active: only while a screen's shared or the server's down", Only: "Mac"},
	{Name: "group-sharing", Group: "Window and sidebar", Value: "false", Default: "false",
		Doc: "true: Share Group in the toolbar, menus and palette, to share Groups with other people", Only: "Mac"},

	// Panes
	{Name: "pane-spacing", Group: "Panes", Value: "spacious | compact", Default: "spacious",
		Doc: "spacious: split panes spaced apart, with rounded corners", Only: "Mac"},
	{Name: "dim-inactive-panes", Group: "Panes", Value: "false", Default: "false",
		Doc: "true: dim the panes you're not typing in, a little", Only: "Mac"},
	{Name: "active-pane-border", Group: "Panes", Value: "false", Default: "false",
		Doc: "true: a border around the focused pane", Only: "Mac"},
	{Name: "active-pane-border-color", Group: "Panes", Value: "accent", Default: "accent",
		Doc: "or #RRGGBB, or light:#…,dark:#…", Only: "Mac"},
	{Name: "pane-animations", Group: "Panes", Value: "true", Default: "true",
		Doc: "false: panes open, close, zoom and float at once", Only: "Mac"},

	// Agents
	{Name: "agent-sleep", Group: "Agents", Value: "off | 3h | 6h | 12h | 24h | 48h", Default: "off",
		Doc: "agents idle that long with nothing running have their process ended to free memory, until you wake them; any duration of a minute or more works, like 90m"},
	{Name: "agent-presence", Group: "Tools", Value: "true", Default: "true",
		Doc: "show agents acting in browser and simulator panes: a cursor, a glow and a badge"},
	{Name: "confirm-close-busy-agent", Group: "Agents", Value: "true", Default: "true",
		Doc: "ask before closing a pane or tab whose agent is at work", Only: "Mac"},

	// Agent view
	{Name: "agent-default-view", Group: "Agent view", Value: "terminal | agent",
		Doc: "which view agent panes open in; unset: terminal on the Mac, agent on the iPhone"},
	{Name: "agent-font-family", Group: "Agent view", Value: "NAME",
		Doc: "the agent view's text; unset: the system font"},
	{Name: "agent-font-size", Group: "Agent view", Value: "14", Default: "14",
		Doc: "the agent view's text size, in points; 18 on the iPhone"},
	{Name: "agent-line-spacing", Group: "Agent view", Value: "3", Default: "3",
		Doc: "the agent view's line spacing, in points; 4 on the iPhone"},
	{Name: "agent-code-font-family", Group: "Agent view", Value: "NAME",
		Doc: "the agent view's code; unset: the terminal's font on the Mac, the system's monospaced one on the iPhone"},
	{Name: "agent-code-font-size", Group: "Agent view", Value: "13", Default: "13",
		Doc: "the agent view's code size, in points; 17 on the iPhone"},
	{Name: "agent-code-wrap", Group: "Agent view", Value: "prose | always | never", Default: "prose",
		Doc: "which code blocks wrap long lines; prose: Markdown, text and tool calls"},
	{Name: "agent-image-size", Group: "Agent view", Value: "original | large | medium | small", Default: "large",
		Doc: "the size pictures you attach in the agent view are sent at"},
	{Name: "agent-image-size-remote", Group: "Agent view", Value: "original | large | medium | small", Default: "medium",
		Doc: "the same, for an agent on a linked computer"},
	{Name: "agent-dictation", Group: "Agent view", Value: "true", Default: "true",
		Doc: "false: no dictation button in the agent view, nor Dictate in the menus"},

	// Notifications
	{Name: "notify-needs-input", Group: "Notifications", Value: "true", Default: "true",
		Doc: "notify me when an agent waits for my input", Only: "Mac"},
	{Name: "notify-permission", Group: "Notifications", Value: "true", Default: "true",
		Doc: "notify me when an agent asks permission", Only: "Mac"},
	{Name: "notify-finished", Group: "Notifications", Value: "true", Default: "true",
		Doc: "notify me when an agent finishes a turn", Only: "Mac"},
	{Name: "notify-error", Group: "Notifications", Value: "true", Default: "true",
		Doc: "notify me when an agent's turn ends in an error", Only: "Mac"},
	{Name: "notify-review-replies", Group: "Notifications", Value: "false", Default: "false",
		Doc: "notify me when an agent answers my diff comments", Only: "Mac"},
	{Name: "notify-program-needs-input", Group: "Notifications", Value: "true", Default: "true",
		Doc: "notify me when another program that reports what it's doing (like brew or your scripts, OSC 7501) waits for my input", Only: "Mac"},
	{Name: "notify-program-finished", Group: "Notifications", Value: "true", Default: "true",
		Doc: "the same, when such a program finishes", Only: "Mac"},
	{Name: "notify-program-error", Group: "Notifications", Value: "true", Default: "true",
		Doc: "the same, when such a program fails", Only: "Mac"},
	{Name: "notify-style", Group: "Notifications", Value: "auto | system | in-app", Default: "auto",
		Doc: "auto: in the window while it's in front, else as macOS notifications", Only: "Mac"},
	{Name: "notify-current-tab", Group: "Notifications", Value: "false", Default: "false",
		Doc: "also notify me about the tab I'm looking at", Only: "Mac"},
	{Name: "dock-badge", Group: "Notifications", Value: "true", Default: "true",
		Doc: "count the agents waiting for me on the Dock icon", Only: "Mac"},
	{Name: "notify-sound-needs-input", Group: "Notifications", Value: "Hammock Chime", Default: "Hammock Chime",
		Doc: "each kind's sound: Hammock Chime, Kalimba, Low, Marimba or Bell, a macOS sound like Glass, or none", Only: "Mac"},
	{Name: "notify-sound-permission", Group: "Notifications", Value: "Hammock Chime", Default: "Hammock Chime", Only: "Mac"},
	{Name: "notify-sound-finished", Group: "Notifications", Value: "Hammock Kalimba", Default: "Hammock Kalimba", Only: "Mac"},
	{Name: "notify-sound-error", Group: "Notifications", Value: "Hammock Low", Default: "Hammock Low", Only: "Mac"},
	{Name: "notify-sound-other", Group: "Notifications", Value: "Hammock Kalimba", Default: "Hammock Kalimba", Only: "Mac"},

	// Keyboard
	{Name: "keybind", Group: "Keyboard", Value: "ctrl+cmd+b=new_browser_tab", Repeat: true,
		Doc:     "one line per shortcut: chord=action; none=action takes an action's shortcut away, chord=unbind frees a chord, digits stands for 1…9, clear drops every default; Settings ▸ Keyboard lists every action's name",
		Example: "keybind = ctrl+cmd+b=new_browser_tab\nkeybind = cmd+j=unbind\nkeybind = none=new_tab\nkeybind = ctrl+cmd+digits=select_tab", Only: "Mac"},

	// Editor panes
	{Name: "editor-wrap", Group: "Editor panes", Value: "false", Default: "false",
		Doc: "wrap long lines instead of scrolling sideways"},
	{Name: "editor-line-numbers", Group: "Editor panes", Value: "true", Default: "true",
		Doc: "line numbers in the gutter"},
	{Name: "editor-tab-width", Group: "Editor panes", Value: "4", Default: "4",
		Doc: "a tab's width; a file's .editorconfig beats it"},
	{Name: "editor-indent", Group: "Editor panes", Value: "spaces | tabs", Default: "spaces",
		Doc: "what Tab inserts; a file's .editorconfig beats it"},
	{Name: "editor-font-family", Group: "Editor panes", Value: "NAME",
		Doc: "editor and diff panes; unset: the terminal's font"},
	{Name: "editor-font-size", Group: "Editor panes", Value: "13",
		Doc: "unset: the terminal's font size"},
	{Name: "editor-vim", Group: "Editor panes", Value: "false", Default: "false",
		Doc: "Vim keys"},
	{Name: "editor-tui-external", Group: "Editor panes", Value: "false", Default: "false",
		Doc: "open editor panes in $VISUAL or $EDITOR; edits from your other devices show there only when it reloads the file", Only: "hmk tui"},

	// Diff panes
	{Name: "diff-style", Group: "Diff panes", Value: "unified | split", Default: "unified",
		Doc: "old and new in one column, or side by side; a pane's own choice beats it"},
	{Name: "diff-whitespace", Group: "Diff panes", Value: "show | ignore", Default: "show",
		Doc: "ignore lines that differ only in their spaces, as git diff -w"},
	{Name: "diff-wrap", Group: "Diff panes", Value: "false", Default: "false",
		Doc: "wrap long lines instead of scrolling sideways"},

	// Browser panes
	{Name: "browser-open-schemes", Group: "Browser panes", Value: "obsidian,zoommtg",
		Doc: "links a browser pane opens in their app without asking, by scheme, with commas; unset: none", Only: "Mac"},
	{Name: "link-open", Group: "Browser panes", Value: "browser | pane | tab", Default: "browser",
		Doc: "links you ⌘-click in a terminal or click in the agent view: your default browser, a Hammock browser pane beside it, or a Hammock browser tab; ⌘⇧-click: the default browser or, set to browser, a pane", Only: "Mac"},
	{Name: "browser-new-tab", Group: "Browser panes", Value: "external | hammock", Default: "external",
		Doc: "links a page opens in a new tab: your default browser, or a Hammock browser tab", Only: "Mac"},

	// Simulator panes
	{Name: "simulator-frame", Group: "Simulator panes", Value: "true", Default: "true",
		Doc: "draw the device around the screen", Only: "Mac"},
	{Name: "simulator-shutdown", Group: "Simulator panes", Value: "true", Default: "true",
		Doc: "simulators Hammock booted shut down a minute after their last pane closes"},

	// Tools
	{Name: "browser-enabled", Group: "Tools", Value: "true", Default: "true",
		Doc: "false: Hammock doesn't offer browser panes, in its menus or to agents"},
	{Name: "simulator-enabled", Group: "Tools", Value: "true", Default: "true",
		Doc: "false: Hammock doesn't offer simulator panes, in its menus or to agents"},
	{Name: "editor-enabled", Group: "Tools", Value: "true", Default: "true",
		Doc: "false: Hammock doesn't offer editor panes, in its menus or to agents"},
	{Name: "diff-enabled", Group: "Tools", Value: "true", Default: "true",
		Doc: "false: Hammock doesn't offer diff panes, in its menus or to agents"},
	{Name: "log-enabled", Group: "Tools", Value: "true", Default: "true",
		Doc: "false: Hammock doesn't offer log panes, in its menus or to agents"},

	// Remote access and linked computers
	{Name: "remote-network", Group: "Remote access and linked computers", Value: "tailscale", Repeat: true,
		Doc:     "one line per network remote access listens on: tailscale, or a subnet like 192.168.1.0/24; none set: Tailscale only",
		Example: "remote-network = tailscale\nremote-network = 192.168.1.0/24"},
	{Name: "remote-address", Group: "Remote access and linked computers", Value: "HOST[:PORT]", Repeat: true,
		Doc: "one line per extra address given out in pairing codes and links: a DNS name, a VPN endpoint, a forwarded port"},
	{Name: "screen-sharing", Group: "Remote access and linked computers", Value: "false", Default: "false",
		Doc: "your iPhone and other Macs may see and control this Mac's screen", Only: "Mac"},
	{Name: "ssh-agent-forwarding", Group: "Remote access and linked computers", Value: "true", Default: "true",
		Doc: "panes on your linked computers use the SSH keys of the one you're at"},
	{Name: "ssh-upstream-agent", Group: "Remote access and linked computers", Value: "PATH",
		Doc: "the SSH agent Hammock's passes requests on to; unset: ~/.ssh/config's IdentityAgent for every host, else SSH_AUTH_SOCK"},

	// Usage limits
	{Name: "usage-limits-AGENT", Group: "Usage limits", Value: "false", Default: "false",
		Doc:     "an agent's usage limits in the sidebar; AGENT is its id: usage-limits-claude, usage-limits-codex, usage-limits-gemini, …",
		Example: "usage-limits-claude = true\nusage-limits-codex = true"},
	{Name: "usage-live", Group: "Usage limits", Value: "false", Default: "false",
		Doc: "agents' usage limits asked of each agent's own service every 5 minutes, with the sign-in its CLI keeps here"},
	{Name: "usage-live-agents", Group: "Usage limits", Value: "claude, codex",
		Doc: "the agents usage-live asks; Settings keeps it to those the sidebar shows; unset: all of them"},

	// TUI
	{Name: "tui-animations", Group: "TUI", Value: "true", Default: "true",
		Doc: "false snaps instead of animating", Only: "hmk tui"},
	{Name: "tui-nerd-font", Group: "TUI", Value: "false", Default: "false",
		Doc: "true shows program icons as Nerd Font glyphs", Only: "hmk tui"},
}

// KeyNamed is the key called name, or nil.
func KeyNamed(name string) *Key {
	for i := range Keys {
		if Keys[i].Name == name {
			return &Keys[i]
		}
	}
	return nil
}

// Header is the commented header a config file Hammock creates starts with:
// every key, a line each, grouped as the docs are.
func Header() string {
	var b strings.Builder
	b.WriteString("# Hammock config (Ghostty syntax). Changes apply automatically.\n")
	b.WriteString("# Every key: " + DocsURL + "reference/config/\n")
	for _, g := range Groups {
		fmt.Fprintf(&b, "#\n# %s\n", g)
		for _, k := range Keys {
			if k.Group != g {
				continue
			}
			line := fmt.Sprintf("#   %s = %s", k.Name, k.Value)
			var notes []string
			if k.Doc != "" {
				notes = append(notes, k.Doc)
			}
			if k.Only != "" {
				notes = append(notes, k.Only+" only")
			}
			if len(notes) > 0 {
				if pad := 40 - len([]rune(line)); pad > 0 {
					line += strings.Repeat(" ", pad)
				}
				line += "   (" + strings.Join(notes, "; ") + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// SwiftHeaderFile is ConfigHeader.generated.swift: the Mac app's copy of
// Header, so a config file says the same whichever wrote it first.
func SwiftHeaderFile() string {
	var b strings.Builder
	b.WriteString("// Generated by `just config-header` from internal/hammockconfig/keys.go. Don't edit.\n\n")
	b.WriteString("extension AppConfig {\n")
	b.WriteString("    /// The commented header a new config file starts with: every key.\n")
	b.WriteString("    static let header = \"\"\"\n")
	for _, l := range strings.Split(Header(), "\n") {
		l = strings.ReplaceAll(l, `\`, `\\`)
		b.WriteString("        " + l + "\n")
	}
	b.WriteString("        \"\"\"\n}\n")
	return b.String()
}
