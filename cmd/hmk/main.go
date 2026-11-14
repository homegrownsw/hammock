// Command hmk is the Hammock CLI: inspect and control the daemon,
// attach to panes, and act as the hook/MCP entry point for agents.
package main

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/daemon"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const usage = `hammock — Hammock CLI

Usage:
  hmk ls [--json] [--on COMPUTER]          list Groups, tabs and panes (--json: for scripts, with
                                            each agent's state and whether you've seen it; --on: only
                                            that linked computer's, or "this")
  hmk focus PANE|TAB                       show a pane (or a tab's focused pane) in the Mac app
  hmk group new [NAME] [--on COMPUTER] [--folder DIR]
                                            create a Group (on a linked computer); --folder: its tabs
                                            open in DIR, and it's named after DIR unless NAME is given
  hmk group rename GROUP NAME
  hmk group icon GROUP [EMOJI]             set (or with no emoji, clear) a Group's emoji
  hmk group icon GROUP --image FILE        set its picture (kept in Hammock; an emoji shows instead if set;
                                            a project's .hammock/icon.png shows when none is) | --no-image
  hmk group folder GROUP [DIR]             show (or set) where a Group's new tabs open; it follows
                                            the folder you last worked in there
  hmk group move GROUP INDEX               move a Group (yours or shared with you) in the sidebar
  hmk group rm GROUP
  hmk grant allow|always|deny ID           answer an agent's permission request
  hmk audit [N]                            show the last N audited agent and remote actions
  hmk key [show|pub]                       this Mac's Hammock Key: an SSH key in its Secure Enclave that
                                            signs only when you approve (Touch ID here, or your phone)
  hmk key create|delete                    make or delete it (Touch ID)
  hmk key allow|disallow DEVICE            let a paired phone approve it (Touch ID), or stop it
  hmk key copy-id [USER@]HOST              add it to a server's authorized_keys (ssh-copy-id)
  hmk ssh setup [--apply] [--apply-jj]     how ssh, git and jj signing in Hammock's panes reach Hammock's agent
                                            (--apply: add Hammock's block to ~/.ssh/config; --apply-jj: add
                                            Hammock's file to jj's config, so jj can sign with the Hammock Key)
  hmk remote on|off                        allow paired devices (and linked computers) on the networks chosen
  hmk remote networks [--json]             the networks remote access may listen on (* chosen), and extra addresses
  hmk remote enable|disable NETWORK        listen on a network, or stop: tailscale, or a subnet (192.168.1.0/24)
  hmk remote address add|remove HOST[:PORT]  an extra address to give out (a DNS name, a VPN endpoint,
                                            a forwarded port)
  hmk pair [--print|--json]                show a pairing code for a new device in the Mac app (off a
                                            Mac, or with --print: print it here as a QR code, outside
                                            agents and shared Groups; --json: the link and a QR PNG)
  hmk devices [revoke ID] [--json]         list or revoke paired devices
  hmk link                                 list your computers on your tailnet (same Tailscale user)
                                            and Hammock on your chosen local networks (Bonjour)
  hmk link COMPUTER|ADDRESS|LINK           link another computer of yours: each shows the other's
                                            Groups (check the code on both; it links with the rest too)
  hmk link --accept | --invite             answer a link asked from another computer (for one
                                            without the app) | print a link to this computer
  hmk link --again COMPUTER                link again one that doesn't link with this one, waits
                                            for its code, or whose certificate changed
  hmk link pending [--json]                computers asking to link here, with their codes
  hmk link confirm|refuse ID|CODE          answer one: confirm only if the code matches the other's
                                            (typing the code shown there confirms the one it matches)
  hmk computers [--json]                   list your linked computers
  hmk computers unlink COMPUTER|this       unlink a computer from all of them (this: this one)
  hmk computers remove COMPUTER            forget one that doesn't link with this one, here only
  hmk computers tint COMPUTER #RRGGBB|none set a computer's badge colour (none: neutral)
  hmk computers icon COMPUTER ICON         set its badge's icon: an SF Symbol's name (macmini), an
                                            emoji, or default (its kind's symbol)
  hmk share GROUP [--new|--off]            print a Group's invite link (--new replaces it, --off stops it)
  hmk share                                list who's in your shared Groups, and who's asking to join
  hmk approve ID drive|view|decline        answer a join request
  hmk unshare [GROUP]                      stop sharing a Group (or every Group: everyone is disconnected)
  hmk join LINK                            join a Group someone shared (waits for them to let you in)
  hmk leave GROUP                          leave a Group shared with you
  hmk tab new [GROUP] [--name N] [--cwd DIR] [--wait] [-- CMD...]
                                            open a tab (in GROUP, by default the one this runs in);
                                            --wait: until CMD ends, exiting with its status (a clean
                                            exit closes the tab, a failure stays open to be read);
                                            125 if its terminal couldn't start
  hmk tab rename TAB NAME
  hmk tab name NAME                        name the tab this runs in (for agents; unnamed tabs are named automatically)
  hmk tab merge TAB INTO [--down]          put a tab's panes beside (or below) another tab's
  hmk tab break PANE                       move a pane out into a tab of its own
  hmk split PANE [--down|--left|--up] [--cwd DIR] [--wait] [-- CMD...]
                                            split a pane (to the right by default); --wait as for tab new
  hmk split PANE --float [--cwd DIR] [--wait] [-- CMD...]
                                            open a pane floating over PANE's tab
  hmk float PANE [--at X,Y,W,H]            lift a pane out of its tab's splits to float over them
                                            (--at: where, in fractions of the tab, e.g. 0.5,0.5,0.45,0.45)
  hmk dock PANE [--beside PANE [--down|--left|--up]]
                                            put a floating pane back in the splits (where it came from,
                                            or beside another pane)
  hmk minimise PANE | restore PANE         put a floating pane on its tab's shelf, or bring it back
  hmk floats TAB                           minimise every floating pane in a tab, or restore them
  hmk browser URL [--group GROUP] [--split PANE [--down]]
                                            open a web page in a browser tab (or split)
  hmk nav PANE URL|back|forward|reload|stop  drive a browser pane
  hmk devtools PANE [--down]               show a browser pane's DevTools in a pane beside it
  hmk sim [DEVICE] [--group G] [--split PANE]  open an iOS simulator in a tab (or split)
  hmk sim list | shot PANE [FILE] [--pixels] [--clipboard] | tap PANE X Y | type PANE TEXT | button PANE NAME | shutdown PANE
  hmk log file PATH | macos | sim [DEVICE] | logcat | -- CMD...  [--group G | --split PANE]
                                            open a log pane (macos, sim: [--process P] [--predicate P]
                                            [--level info|debug]; logcat: [--serial S])
  hmk log read PANE [TEXT] [--level L,...] [--process P] [--tag T] [--limit N] [--follow] [--json]
                                            a log pane's entries, filtered (TEXT: words, or /regex/)
  hmk log clear PANE                       forget a log pane's entries
  hmk edit FILE[:LINE[:COL][-LINE[:COL]]] [--to LINE[:COL]] [--down | --tab | --split PANE | --group G] [--large]
                                            open a file in an editor pane beside this one (--tab: in a new
                                            tab; outside a pane, always), with those lines selected; one in
                                            the tab already on that file is reused; --large: over 5 MB
  hmk edit --read PANE [--numbers] [--lines FROM-TO] [--json]
                                            an editor pane's text, unsaved edits included (--json: with its
                                            file, whether it's saved, and the selection)
  hmk edit --select PANE LINE[:COL][-LINE[:COL]]
                                            select lines in an editor pane (LINE alone: all of it) and scroll to them
  hmk diff [REV | FROM..TO | FROM...TO] [--uncommitted | --branch [--base B] [--with-uncommitted]]
           [--repo DIR] [--split | --unified] [--ignore-whitespace] [--beside PANE [--down] | --group G]
           [--name NAME] [--wait]
                                            open a git diff to review in a new tab after this pane's
                                            (--beside: split beside a pane). With no REV: this branch
                                            against its base, uncommitted changes too, or on the default
                                            branch what's uncommitted; REV: one commit; --wait: until closed
  hmk review [list [--all] | show [REVIEW|PANE] [--all-threads]] [--json]
                                            the comment threads on diffs: yours (sent to you, or on diffs
                                            you opened), open ones
  hmk review reply THREAD TEXT | address THREAD [NOTE] | comment [REVIEW|PANE] FILE:LINE[-LINE] TEXT
                                            answer a thread, mark it addressed (the user resolves it), or
                                            start one on a diff's new side
  hmk review wait [REVIEW|PANE] [--timeout S]   until the user sends comments or resolves threads
  hmk attach PANE                          attach this terminal (Ctrl-] detaches)
  hmk send PANE [TEXT] [--key NAME]... [--enter]
                                            type TEXT into a pane, then press keys (escape, ctrl-c,
                                            up, f2…) and Enter, each on its own
  hmk read PANE [--scrollback]             print a pane's screen as text (an editor pane's: its text)
  hmk close PANE|TAB|GROUP
  hmk reopen                               reopen the last tab or pane you closed (agents resume their sessions)
  hmk agent AGENT [--dir DIR] [--group GROUP] [--on COMPUTER] [PROMPT]
                                            start an agent in a new tab (in DIR, by default here), in GROUP
                                            or the folder's Group (or a new one); --on a linked computer
                                            (there, DIR is absolute or ~/…, its home; by default ~)
  hmk switch PANE AGENT                    continue a pane's agent session in another agent
  hmk sleep [PANE]                         put a pane's idle agent to sleep, by default this pane's:
                                            its process ends, freeing its memory, until it's woken (the
                                            apps' Wake button, typing in it, or hmk wake); agent-sleep in
                                            the config file does it by itself
  hmk wake [PANE]                          wake a sleeping agent now
  hmk session-link [PANE]                  print the web link to a pane's agent session (Claude Code's
                                            Remote Control), by default this pane's
  hmk schedule [ls]                        list scheduled tasks: an agent's prompt, or a command, run
                                            on a schedule, each run a new tab in its Group
  hmk schedule add [NAME] (--at HH:MM [--days weekdays|weekends|mon,tue…] | --cron EXPR)
             [--claude|--codex|--AGENT|--command] [--dir DIR] [--group G] [OPTIONS] -- PROMPT|COMMAND
                                            add one (an agent's add or edit asks you first; hmk
                                            schedule help lists the options)
  hmk schedule edit|show|run|pause|resume|skip|rm SCHEDULE
                                            change, see (its runs), run now, pause, skip the next run, delete
  hmk show FILE|URL [--pane PANE | --split PANE]
                                            open a file (HTML, Markdown, an image, a PDF) or a page in a
                                            browser pane beside this one; again from this pane, it reloads
                                            in that one (or one in this tab already on that page);
                                            --pane: in that browser pane; --split: beside that pane
  hmk sessions [QUERY] [--limit N] [--json] [--folder DIR] [--group GROUP] [--everywhere] [--on COMPUTER]
                                            find agents' sessions on this Mac, or a linked
                                            computer (an agent: its project's; --everywhere asks you);
                                            --group: only those in the folders open in GROUP
  hmk sessions read ID [QUERY] [--limit N]  a session's messages matching QUERY (with those around them), or its latest
                                            (every word, by its start, in titles, folders and conversations)
  hmk resume ID [--in AGENT] [--group GROUP] [--on COMPUTER]
                                            resume a session in a new tab in its folder's Group
                                            (or in the other agent, converted); --on: a linked computer's
  hmk theme [NAME]                         list Hammock's themes (* the one in use), or use one (any case;
                                            custom: your own terminal themes, window theme, agent colour, pane
                                            border, sidebar and toolbar colours; those set before, else the current theme's)
  hmk hook HARNESS [EVENT]                 forward an agent hook event (stdin or JSON arg)
  hmk hooks print                          show hook config for Claude Code and Codex (install covers the rest)
  hmk hooks install [--dry-run [--json]] [--harness claude,codex,…]  add Hammock hooks to your agents' config
  hmk hooks install --uninstall            remove them again
  hmk mcp                                  run the MCP server on stdio
  hmk tui                                  full-screen UI with mouse support (Ctrl-B prefix, Ctrl-B ? for keys)
  hmk report STATE [MESSAGE] [--progress N] [--id ID] [--title T] [--app NAME] [--kind KIND]
                                            tell the terminal what this script is doing (OSC 7501): idle,
                                            working, done, blocked (on you: --kind permission|question|auth),
                                            error, or clear; Hammock shows it on the pane and in Needs You
  hmk stop [--panes] | start | restart     stop or start the Hammock Server (panes keep running unless
                                            --panes, which ends them too; an open Hammock app starts it again)
  hmk status [--json]                      whether the Hammock Server is running, and its Groups, remote
                                            access and linked computers (exits 3 when it isn't running)
  hmk service install|uninstall|status     Linux: run the Hammock Server as a systemd user service, from boot
  hmk acknowledgements [NAME | --all]      the third-party software in Hammock and its licences
                                            (NAME: one's licence text; --all: every one's)
  hmk docs [--print]                       open Hammock's documentation in your browser (--print, or
                                            where nothing can open it: print its address)

IDs may be abbreviated to any unique prefix; Groups and tabs can also be
named. Set HMK_DIR to use a non-default state directory.
`

func main() {
	switch filepath.Base(os.Args[0]) {
	case "hmk-ssh-sign":
		os.Exit(runSSHSign(os.Args[1:], ownSSHProgram())) // git's gpg.ssh.program in Hammock's panes (hammockkey.go)
	case "hmk-jj-ssh-sign":
		os.Exit(runSSHSign(os.Args[1:], ownJJProgram())) // jj's signing.backends.ssh.program, likewise
	}
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return
	}
	var err error
	switch args[0] {
	case "hook":
		err = runHook(args[1:])
	case "hooks":
		err = runHooks(args[1:])
	case "mcp":
		err = runMCP(args[1:])
	case "tui":
		err = runTUI()
	case "audit":
		err = runAudit(args[1:])
	case "key":
		err = runKey(args[1:])
	case "ssh":
		err = runSSH(args[1:])
	case "agent":
		err = runAgent(args[1:])
	case "schedule", "schedules":
		err = runSchedule(args[1:])
	case "show":
		err = runShow(args[1:])
	case "theme":
		err = runTheme(args[1:])
	case "acknowledgements", "licences", "licenses":
		err = runAcknowledgements(args[1:])
	case "docs":
		err = runDocs(args[1:])
	case "stop":
		err = runStop(args[1:])
	case "start":
		err = runStart()
	case "restart":
		err = runRestart()
	case "status":
		err = runStatus(args[1:])
	case "report":
		err = runReport(args[1:])
	case "service":
		err = runService(args[1:])
	default:
		err = runCommand(args)
	}
	if errors.Is(err, errNotRunning) {
		os.Exit(3)
	}
	var status exitStatus
	if errors.As(err, &status) {
		os.Exit(int(status)) // --wait: the command's own
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "hammock:", termSafeText(err.Error()))
		os.Exit(1)
	}
}

func socketPath() string {
	if s := paneEnv("SOCKET"); s != "" {
		return s
	}
	return daemon.SocketPath(daemon.DefaultDir())
}

func connect(ctx context.Context) (*client.Client, error) {
	c, err := client.Dial(ctx, socketPath(), "cli", "hmk")
	if err != nil {
		return nil, fmt.Errorf("cannot reach hmkd at %s: %w", socketPath(), err)
	}
	c.OnApproval = waitingForYou
	return c, nil
}

// waitingForYou says, on stderr, that what was asked waits for you: what
// only you may do, asked from outside the Hammock app, is yours to allow
// there or on your phone.
func waitingForYou(ap *pb.ApprovalPending) {
	fmt.Fprintf(os.Stderr, "Waiting for you to allow this in the Hammock app (open it if it isn't) or on your phone (%s)…\n", termSafe(ap.Headline))
}

// world is a resolved view of the daemon's state.
type world struct {
	groups []*pb.Group
	tabs   map[string]*pb.Tab
	panes  map[string]*pb.Pane
	attn   []*pb.AttentionItem
	grants []*pb.GrantRequest
	joins  []*pb.JoinRequest
	people map[string]*pb.Person
	// Linked computers (this one first).
	computers *pb.Computers
	linkReqs  []*pb.LinkRequest // link requests showing a code when this connected
	schedules []*pb.Schedule    // scheduled tasks, this computer's and linked ones'
}

func (w *world) group(id string) *pb.Group {
	for _, s := range w.groups {
		if s.Id == id {
			return s
		}
	}
	return nil
}

func fetchWorld(ctx context.Context, c *client.Client) (*world, error) {
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	if err != nil {
		return nil, err
	}
	st := r.GetState()
	w := &world{groups: st.Groups, tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{}, attn: st.Attention, grants: st.Grants,
		joins: st.JoinRequests, people: map[string]*pb.Person{}, computers: st.Computers, linkReqs: st.LinkRequests, schedules: st.Schedules}
	for _, p := range st.People {
		w.people[p.UserId] = p
	}
	for _, t := range st.Tabs {
		w.tabs[t.Id] = t
	}
	for _, p := range st.Panes {
		w.panes[p.Id] = p
	}
	return w, nil
}

// resolve finds an entity by exact id, unique id prefix, or (groups/tabs) name.
func (w *world) resolve(ref, kind string) (string, error) {
	var matches []string
	add := func(id, name string) {
		if id == ref || name == ref || strings.HasPrefix(id, ref) {
			matches = append(matches, id)
		}
	}
	if kind == "" || kind == "group" {
		for _, s := range w.groups {
			add(s.Id, client.GroupTitle(s, w.groups, w.panes))
		}
	}
	if kind == "" || kind == "tab" {
		for _, t := range w.tabs {
			add(t.Id, client.TabTitle(t, w.group(t.GroupId)))
		}
	}
	if kind == "" || kind == "pane" {
		for _, p := range w.panes {
			add(p.Id, "")
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("nothing matches %q", ref)
	case 1:
		return matches[0], nil
	}
	return "", fmt.Errorf("%q is ambiguous: %s", ref, strings.Join(matches, ", "))
}

func runCommand(args []string) error {
	wait := 15 * time.Second
	if len(args) > 1 && (args[0] == "sim" || args[0] == "simulator") && (args[1] == "rotate" || args[1] == "pinch" || args[1] == "fold") {
		wait = 4 * time.Minute // the first one starts the device's XCTest runner
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	w, err := fetchWorld(ctx, c)
	if err != nil {
		return err
	}
	call := func(m *pb.ClientMessage) (*pb.ServerMessage, error) {
		r, err := c.Call(ctx, m)
		if err != nil && strings.HasPrefix(err.Error(), "out of scope") {
			// Run by an agent, beyond what it may do on its own: ask the
			// person (Needs You, the phone, hmk grant), and if they allow
			// it, try again.
			if action, target := grantFor(m); target != "" {
				return askAndRetry(c, m, action, target, err)
			}
		}
		return r, err
	}

	// --on COMPUTER: a linked computer's sessions (the daemon passes it on).
	// hmk resume checks it against --group itself.
	on, onGiven := "", false
	if (args[0] == "sessions" || args[0] == "resume") && flagValue(args, "--on") != "" {
		cid, err := w.onComputer(args)
		if err != nil {
			return err
		}
		on, onGiven = cid, true
		args = dropFlag(args, "--on")
		call = onComputerCalls(call, cid)
	}

	switch args[0] {
	case "link":
		return runLink(c, w, args[1:])

	case "computers":
		return runComputers(w, call, args[1:])

	case "ls":
		if on := flagValue(args, "--on"); on != "" {
			cmp, err := w.computer(on)
			if err != nil {
				return err
			}
			host := cmp.UserId
			if cmp.ThisComputer {
				host = ""
			}
			w.groups = filterGroups(w.groups, func(g *pb.Group) bool { return g.HostUserId == host })
		}
		if hasFlag(args, "--json") {
			return printWorldJSON(w)
		}
		printWorld(w)
		return nil

	case "focus":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk focus PANE|TAB")
		}
		return focusInApp(w, args[1])

	case "group":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk group new|rename|rm")
		}
		switch args[1] {
		case "new":
			cid, err := w.onComputer(args)
			if err != nil {
				return err
			}
			name := strings.Join(dropFlag(dropFlag(args[2:], "--on"), "--folder"), " ")
			// --folder: where its tabs open (a path on that computer with
			// --on); named after it unless NAME says otherwise.
			dir := flagValue(args, "--folder")
			if dir != "" && cid == "" {
				if dir, err = filepath.Abs(dir); err != nil {
					return err
				}
			}
			if name == "" && dir != "" {
				name = filepath.Base(dir)
			}
			r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: name, ComputerId: cid}}})
			if err != nil {
				return err
			}
			id := client.CreatedID(r)
			if dir != "" {
				// One made on another computer comes back from it a moment later.
				set := &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: id, Folder: dir}}}
				_, err = call(set)
				for i := 0; err != nil && cid != "" && i < 50; i++ {
					time.Sleep(40 * time.Millisecond)
					_, err = call(set)
				}
				if err != nil {
					return err
				}
			}
			fmt.Println(id)
			return nil
		case "rename":
			if len(args) < 4 {
				return fmt.Errorf("usage: hmk group rename GROUP NAME")
			}
			id, err := w.resolve(args[2], "group")
			if err != nil {
				return err
			}
			_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_RenameGroup{RenameGroup: &pb.RenameGroup{GroupId: id, Name: strings.Join(args[3:], " ")}}})
			return err
		case "icon":
			if len(args) < 3 {
				return fmt.Errorf("usage: hmk group icon GROUP [EMOJI | --image FILE | --no-image]")
			}
			id, err := w.resolve(args[2], "group")
			if err != nil {
				return err
			}
			if hasFlag(args, "--no-image") {
				_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupIconImage{SetGroupIconImage: &pb.SetGroupIconImage{GroupId: id}}})
				return err
			}
			if f := flagValue(args, "--image"); f != "" {
				// Kept with the daemon's data; an emoji still wins.
				data, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupIconImage{SetGroupIconImage: &pb.SetGroupIconImage{GroupId: id, Image: data}}})
				return err
			}
			icon := ""
			if len(args) > 3 {
				icon = args[3]
			}
			_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupIcon{SetGroupIcon: &pb.SetGroupIcon{GroupId: id, Icon: icon}}})
			return err
		case "folder":
			// Where its new tabs open; it follows its terminals as you work.
			if len(args) < 3 {
				return fmt.Errorf("usage: hmk group folder GROUP [DIR]")
			}
			id, err := w.resolve(args[2], "group")
			if err != nil {
				return err
			}
			if len(args) < 4 {
				for _, g := range w.groups {
					if g.Id == id {
						printValue(g.Folder)
					}
				}
				return nil
			}
			dir, err := filepath.Abs(args[3])
			if err != nil {
				return err
			}
			_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: id, Folder: dir}}})
			return err
		case "move":
			if len(args) < 4 {
				return fmt.Errorf("usage: hmk group move GROUP INDEX")
			}
			id, err := w.resolve(args[2], "group")
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(args[3])
			if err != nil {
				return fmt.Errorf("INDEX must be a number")
			}
			_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_MoveGroup{MoveGroup: &pb.MoveGroup{GroupId: id, Index: int32(n)}}})
			return err
		case "rm":
			if len(args) < 3 {
				return fmt.Errorf("usage: hmk group rm GROUP")
			}
			id, err := w.resolve(args[2], "group")
			if err != nil {
				return err
			}
			_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: id}}})
			return err
		}

	case "tab":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk tab new|rename|name|merge|break")
		}
		switch args[1] {
		case "new":
			flags, cmd := splitCmd(args[2:])
			var sid string
			if len(flags) > 0 && !strings.HasPrefix(flags[0], "-") {
				id, err := w.resolve(flags[0], "group")
				if err != nil {
					return err
				}
				sid = id
			} else if sid = w.groupOfPane(paneEnv("PANE_ID")); sid == "" {
				// No GROUP: the one this runs in.
				return fmt.Errorf("usage: hmk tab new GROUP [--name N] [--cwd DIR] [--wait] [-- CMD...] (GROUP may be left out inside a Hammock pane)")
			}
			if hasFlag(flags, "--wait") && len(cmd) == 0 {
				return fmt.Errorf("--wait needs a command: hmk tab new --wait -- CMD...")
			}
			name, cwd := flagValue(flags, "--name"), flagValue(flags, "--cwd")
			r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
				GroupId: sid, Name: name, Pane: &pb.PaneSpec{Command: cmd, Cwd: absDir(cwd)},
			}}})
			if err != nil {
				return err
			}
			tid := client.CreatedID(r)
			fmt.Println(tid)
			if !hasFlag(flags, "--wait") {
				return nil
			}
			w2, err := fetchWorld(ctx, c)
			if err != nil {
				return err
			}
			t := w2.tabs[tid]
			if t == nil {
				return nil // already done and closed
			}
			return waitForExit(c, t.FocusedPaneId)
		case "name":
			// For agents: name the tab you're running in.
			if len(args) < 3 {
				return fmt.Errorf("usage: hmk tab name NAME (inside a Hammock pane)")
			}
			p := w.panes[paneEnv("PANE_ID")]
			if p == nil {
				return fmt.Errorf("not inside a Hammock pane (HMK_PANE_ID isn't set)")
			}
			_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_RenameTab{RenameTab: &pb.RenameTab{TabId: p.TabId, Name: strings.Join(args[2:], " ")}}})
			return err
		case "rename":
			if len(args) < 4 {
				return fmt.Errorf("usage: hmk tab rename TAB NAME")
			}
			id, err := w.resolve(args[2], "tab")
			if err != nil {
				return err
			}
			_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_RenameTab{RenameTab: &pb.RenameTab{TabId: id, Name: strings.Join(args[3:], " ")}}})
			return err
		case "merge":
			if len(args) < 4 {
				return fmt.Errorf("usage: hmk tab merge TAB INTO [--down]  (INTO: a tab, or a pane to sit beside)")
			}
			id, err := w.resolve(args[2], "tab")
			if err != nil {
				return err
			}
			into, err := w.resolve(args[3], "")
			if err != nil {
				return err
			}
			for _, t := range w.tabs {
				if t.Id == into {
					into = t.FocusedPaneId
				}
			}
			dir := pb.Split_DIRECTION_HORIZONTAL
			if hasFlag(args[4:], "--down") {
				dir = pb.Split_DIRECTION_VERTICAL
			}
			_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_MergeTab{MergeTab: &pb.MergeTab{TabId: id, IntoPaneId: into, Direction: dir}}})
			return err
		case "break":
			if len(args) < 3 {
				return fmt.Errorf("usage: hmk tab break PANE")
			}
			pid, err := w.resolve(args[2], "pane")
			if err != nil {
				return err
			}
			r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_BreakOutPane{BreakOutPane: &pb.BreakOutPane{PaneId: pid}}})
			if err != nil {
				return err
			}
			fmt.Println(client.CreatedID(r))
			return nil
		}

	case "split":
		flags, cmd := splitCmd(args[1:])
		if len(flags) < 1 {
			return fmt.Errorf("usage: hmk split PANE [--down|--left|--up|--float] [--cwd DIR] [--wait] [-- CMD...]")
		}
		pid, err := w.resolve(flags[0], "pane")
		if err != nil {
			return err
		}
		wait := hasFlag(flags, "--wait")
		if wait && len(cmd) == 0 {
			return fmt.Errorf("--wait needs a command: hmk split PANE --wait -- CMD...")
		}
		// --wait: block until the new pane's command ends, exiting with its status.
		done := func(r *pb.ServerMessage) error {
			id := client.CreatedID(r)
			fmt.Println(id)
			if !wait {
				return nil
			}
			return waitForExit(c, id)
		}
		if hasFlag(flags, "--float") {
			r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{
				SplitPaneId: pid, Floating: true, Spec: &pb.PaneSpec{Command: cmd, Cwd: absDir(flagValue(flags, "--cwd"))},
			}}})
			if err != nil {
				return err
			}
			return done(r)
		}
		dir := pb.Split_DIRECTION_HORIZONTAL
		if hasFlag(flags, "--down") || hasFlag(flags, "--up") {
			dir = pb.Split_DIRECTION_VERTICAL
		}
		before := hasFlag(flags, "--left") || hasFlag(flags, "--up")
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{
			SplitPaneId: pid, Direction: dir, Before: before, Spec: &pb.PaneSpec{Command: cmd, Cwd: absDir(flagValue(flags, "--cwd"))},
		}}})
		if err != nil {
			return err
		}
		return done(r)

	case "browser":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk browser URL [--group GROUP] [--split PANE [--down]]")
		}
		spec := &pb.PaneSpec{Url: args[1], CreatedByPaneId: paneEnv("PANE_ID")}
		var m *pb.ClientMessage
		if ref := flagValue(args[2:], "--split"); ref != "" {
			pid, err := w.resolve(ref, "pane")
			if err != nil {
				return err
			}
			dir := pb.Split_DIRECTION_HORIZONTAL
			if hasFlag(args, "--down") {
				dir = pb.Split_DIRECTION_VERTICAL
			}
			m = &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: pid, Direction: dir, Spec: spec}}}
		} else {
			sid, err := w.browserGroup(flagValue(args[2:], "--group"))
			if err != nil {
				return err
			}
			m = &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Pane: spec}}}
		}
		r, err := call(m)
		if err != nil {
			return err
		}
		fmt.Println(client.CreatedID(r))
		return nil

	case "sim", "simulator":
		return runSim(w, call, args)

	case "log":
		return runLog(w, call, args)

	case "edit":
		return runEdit(w, call, args)

	case "diff":
		return runDiff(w, c, call, args)

	case "review":
		return runReview(w, c, call, args)

	case "nav":
		if len(args) < 3 {
			return fmt.Errorf("usage: hmk nav PANE URL|back|forward|reload|stop")
		}
		pid, err := w.resolve(args[1], "pane")
		if err != nil {
			return err
		}
		nav := &pb.BrowserNavigate{PaneId: pid, Action: args[2]}
		switch args[2] {
		case "back", "forward", "reload", "stop":
		default:
			nav.Action, nav.Url = "load", strings.Join(args[2:], " ")
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_BrowserNavigate{BrowserNavigate: nav}})
		return err

	case "attach":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk attach PANE")
		}
		pid, err := w.resolve(args[1], "pane")
		if err != nil {
			return err
		}
		cancel() // attach runs without the command timeout
		return attach(c, pid)

	case "send":
		if len(args) < 3 {
			return fmt.Errorf("usage: hmk send PANE [TEXT] [--key NAME]... [--enter]")
		}
		pid, err := w.resolve(args[1], "pane")
		if err != nil {
			return err
		}
		text, keys, enter, err := sendFlags(args[2:])
		if err != nil {
			return err
		}
		writes, err := typingWrites(text, keys, enter)
		if err != nil {
			return err
		}
		return typeInto(writes, func(b string) error {
			_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pid, Data: []byte(b)}}})
			return err
		})

	case "read":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk read PANE [--scrollback]")
		}
		pid, err := w.resolve(args[1], "pane")
		if err != nil {
			return err
		}
		if p := w.panes[pid]; p.Kind == pb.PaneKind_PANE_KIND_EDITOR {
			return runEditRead(w, call, pid, nil) // no screen: its buffer
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReadPane{ReadPane: &pb.ReadPane{PaneId: pid, Scrollback: hasFlag(args[2:], "--scrollback")}}})
		if err != nil {
			return err
		}
		fmt.Println(r.GetPaneText().Text)
		return nil

	case "grant":
		if len(args) < 3 {
			return fmt.Errorf("usage: hmk grant allow|always|deny GRANT_ID")
		}
		dec := map[string]pb.RespondGrant_Decision{"allow": pb.RespondGrant_DECISION_ALLOW_ONCE, "always": pb.RespondGrant_DECISION_ALLOW_GROUP, "deny": pb.RespondGrant_DECISION_DENY}[args[1]]
		if dec == 0 {
			return fmt.Errorf("decision must be allow, always or deny")
		}
		_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: args[2], Decision: dec}}})
		return err

	case "switch":
		if len(args) < 3 {
			return fmt.Errorf("usage: hmk switch PANE AGENT")
		}
		pid, err := w.resolve(args[1], "pane")
		if err != nil {
			return err
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_SwitchAgent{SwitchAgent: &pb.SwitchAgent{PaneId: pid, TargetHarness: args[2]}}})
		if err != nil {
			return err
		}
		if rep := r.GetAgentSwitched().GetReport(); rep != "" {
			printlnSafe(rep)
		}
		return nil

	case "sleep", "wake":
		pid := paneEnv("PANE_ID")
		if len(args) > 1 {
			var err error
			if pid, err = w.resolve(args[1], "pane"); err != nil {
				return err
			}
		}
		if pid == "" {
			return fmt.Errorf("usage: hmk %s PANE (or run it inside a Hammock pane)", args[0])
		}
		_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_SleepPane{SleepPane: &pb.SleepPane{PaneId: pid, Wake: args[0] == "wake"}}})
		return err

	case "session-link":
		pid := paneEnv("PANE_ID")
		if len(args) > 1 {
			var err error
			if pid, err = w.resolve(args[1], "pane"); err != nil {
				return err
			}
		}
		p := w.panes[pid]
		if p == nil {
			return fmt.Errorf("usage: hmk session-link PANE (or run it inside a Hammock pane)")
		}
		u := p.GetAgent().GetSessionUrl()
		if u == "" {
			return fmt.Errorf("no session link: the pane's agent has none (Claude Code's comes with Remote Control)")
		}
		fmt.Println(u)
		return nil

	case "devtools":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk devtools PANE [--down]")
		}
		pid, err := w.resolve(args[1], "pane")
		if err != nil {
			return err
		}
		dir := pb.Split_DIRECTION_HORIZONTAL
		if hasFlag(args, "--down") {
			dir = pb.Split_DIRECTION_VERTICAL
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: pid, Direction: dir,
			Spec: &pb.PaneSpec{Url: daemon.DevToolsURL + pid}}}})
		if err != nil {
			return err
		}
		fmt.Println(client.CreatedID(r))
		return nil

	case "sessions":
		return runSessions(call, w, args[1:], on, onGiven)

	case "resume":
		return runResume(call, w, args[1:], on, onGiven)

	case "remote":
		return runRemote(call, args[1:])

	case "pair":
		asJSON := hasFlag(args, "--json")
		if !hasFlag(args, "--print") && !asJSON {
			// The secret stays on the Mac's screen, out of terminals that
			// agents or guests can read. Off a Mac there's no app to show
			// it: it's printed (the daemon gives it only to you, never to
			// an agent in a pane).
			r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ShowPairing{ShowPairing: &pb.ShowPairing{}}})
			if err == nil {
				printfSafe("Showing the pairing code on %s.\n", r.GetShowPairing().GetHostName())
				return nil
			}
			if runtime.GOOS == "darwin" {
				return err
			}
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}})
		if err != nil {
			return err
		}
		p := r.GetPairing()
		b, _ := proto.Marshal(p)
		link := "hammock://pair/" + base64.RawURLEncoding.EncodeToString(b)
		if asJSON {
			png, err := qrPNG(link)
			if err != nil {
				return err
			}
			return printJSON(map[string]any{"link": link, "host": p.HostName, "addresses": p.Addresses,
				"cert_sha256": p.CertSha256, "remote_enabled": p.RemoteEnabled, "qr_png": png})
		}
		if !p.RemoteEnabled {
			fmt.Println("note: remote access is off; turn it on with: hmk remote on")
		}
		fmt.Println("Pairing code (valid 10 minutes, single use): scan it in Hammock on your iPhone (Devices ▸ Add Mac).")
		if q, err := qrText(link); err == nil && stdoutIsTerminal() {
			fmt.Print("\n" + q)
		}
		printfSafe("\nOr paste it there:\n\n  %s\n\nHost %s, addresses %s\nCertificate SHA-256 %s\n", link, p.HostName, strings.Join(p.Addresses, ", "), p.CertSha256)
		return nil

	case "share":
		if len(args) < 2 {
			for _, g := range w.groups {
				if len(g.Members) == 0 && !g.InviteActive {
					continue
				}
				printfSafe("%s  %s%s\n", g.Id, client.GroupTitle(g, w.groups, w.panes), map[bool]string{true: "  (invite link on)"}[g.InviteActive])
				for _, m := range g.Members {
					if m.Role == pb.Role_ROLE_HOST {
						continue
					}
					p := w.people[m.UserId]
					printfSafe("  %-8s %s (%s)%s\n", strings.TrimPrefix(strings.ToLower(m.Role.String()), "role_"), p.GetName(), p.GetDeviceName(), map[bool]string{true: "  here"}[m.Online])
				}
			}
			for _, j := range w.joins {
				p := w.people[j.UserId]
				printfSafe("asking to join %s: %s (%s)  →  hmk approve %s drive|view|decline\n", j.GroupId, p.GetName(), p.GetDeviceName(), j.Id)
				if j.KeyFingerprint != "" {
					printfSafe("  key %s%s\n", j.KeyFingerprint, joinKeyWarning(j, p.GetName()))
				}
			}
			return nil
		}
		id, err := w.resolve(args[1], "group")
		if err != nil {
			return err
		}
		if hasFlag(args, "--off") {
			_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_RevokeInvite{RevokeInvite: &pb.RevokeInvite{GroupId: id}}})
			return err
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: id, Regenerate: hasFlag(args, "--new")}}})
		if err != nil {
			return err
		}
		fmt.Println(r.GetInvite().Link)
		return nil

	case "approve":
		if len(args) < 3 {
			return fmt.Errorf("usage: hmk approve ID drive|view|decline")
		}
		role, ok := map[string]pb.Role{"drive": pb.Role_ROLE_DRIVER, "view": pb.Role_ROLE_VIEWER, "decline": pb.Role_ROLE_UNSPECIFIED}[strings.ToLower(args[2])]
		if !ok {
			return fmt.Errorf("hmk approve: %q isn't drive, view or decline", args[2])
		}
		id := args[1]
		for _, j := range w.joins {
			if strings.HasPrefix(j.Id, id) {
				id = j.Id
			}
		}
		_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_RespondJoin{RespondJoin: &pb.RespondJoin{Id: id, Role: role}}})
		return err

	case "unshare":
		id := ""
		if len(args) > 1 {
			var err error
			if id, err = w.resolve(args[1], "group"); err != nil {
				return err
			}
		}
		_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_StopSharing{StopSharing: &pb.StopSharing{GroupId: id}}})
		return err

	case "join":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk join LINK")
		}
		fmt.Fprintln(os.Stderr, "Asking to join; waiting for the host to let you in…")
		jctx, jcancel := context.WithTimeout(context.Background(), time.Hour)
		defer jcancel()
		r, err := c.Call(jctx, &pb.ClientMessage{Msg: &pb.ClientMessage_JoinShared{JoinShared: &pb.JoinShared{Link: args[1]}}})
		if err != nil {
			return err
		}
		printValue(client.CreatedID(r))
		return nil

	case "leave":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk leave GROUP")
		}
		id, err := w.resolve(args[1], "group")
		if err != nil {
			return err
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_LeaveShared{LeaveShared: &pb.LeaveShared{GroupId: id}}})
		return err

	case "devices":
		if len(args) >= 3 && args[1] == "revoke" {
			_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_RevokeDevice{RevokeDevice: &pb.RevokeDevice{DeviceId: args[2]}}})
			return err
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ListDevices{ListDevices: &pb.ListDevices{}}})
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return printProtoJSON(r.GetDevices())
		}
		for _, d := range r.GetDevices().Devices {
			printfSafe("%s  %-20s %-6s paired %s, last seen %s\n", d.Id, d.Name, d.Platform, time.UnixMilli(d.PairedUnixMs).Format("2006-01-02"), time.UnixMilli(d.LastSeenUnixMs).Format("2006-01-02 15:04"))
		}
		if o := r.GetDevices().OtherTailscaleRange; len(o) > 0 {
			fmt.Printf("note: another network uses Tailscale's address range (%s); remote access listens only on Tailscale's own\n", strings.Join(o, ", "))
		} else if r.GetDevices().TailscaleUnknown {
			fmt.Println("note: several networks use Tailscale's address range and Tailscale couldn't be asked which is its own; remote access listens on all of them")
		}
		return nil

	case "reopen":
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}})
		if err != nil {
			return err
		}
		fmt.Println(client.CreatedID(r))
		return nil

	case "float", "dock", "minimise", "minimize", "restore":
		if len(args) < 2 {
			return fmt.Errorf("usage: hammock %s PANE (see hmk --help)", args[0])
		}
		pid, err := w.resolve(args[1], "pane")
		if err != nil {
			return err
		}
		m := &pb.FloatPane{PaneId: pid}
		switch args[0] {
		case "float":
			m.Action = pb.FloatPane_ACTION_FLOAT
			if at := flagValue(args[2:], "--at"); at != "" {
				if m.Frame, err = parseFrame(at); err != nil {
					return err
				}
			}
		case "dock":
			m.Action = pb.FloatPane_ACTION_DOCK
			if ref := flagValue(args[2:], "--beside"); ref != "" {
				if m.TargetPaneId, err = w.resolve(ref, "pane"); err != nil {
					return err
				}
				m.Direction = pb.Split_DIRECTION_HORIZONTAL
				if hasFlag(args, "--down") || hasFlag(args, "--up") {
					m.Direction = pb.Split_DIRECTION_VERTICAL
				}
				m.Before = hasFlag(args, "--left") || hasFlag(args, "--up")
			}
		case "minimise", "minimize":
			m.Action = pb.FloatPane_ACTION_MINIMISE
		case "restore":
			m.Action = pb.FloatPane_ACTION_RESTORE
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_FloatPane{FloatPane: m}})
		return err

	case "floats":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk floats TAB")
		}
		tid, err := w.resolve(args[1], "tab")
		if err != nil {
			return err
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_FloatPane{FloatPane: &pb.FloatPane{Action: pb.FloatPane_ACTION_TOGGLE_ALL, TabId: tid}}})
		return err

	case "close":
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk close PANE|TAB|GROUP")
		}
		id, err := w.resolve(args[1], "")
		if err != nil {
			return err
		}
		var m *pb.ClientMessage
		switch {
		case strings.HasPrefix(id, "pn_"):
			m = &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: id}}}
		case strings.HasPrefix(id, "tb_"):
			m = &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: id}}}
		default:
			m = &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: id}}}
		}
		_, err = call(m)
		return err
	}
	return fmt.Errorf("unknown command %q (see hmk --help)", strings.Join(args, " "))
}

// splitCmd separates flags/positionals from a trailing "-- CMD..." command.
func splitCmd(args []string) (flags, cmd []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func absDir(d string) string {
	if d == "" {
		return ""
	}
	if strings.HasPrefix(d, "~/") {
		home, _ := os.UserHomeDir()
		return home + d[1:]
	}
	if !filepath.IsAbs(d) {
		wd, _ := os.Getwd()
		d = filepath.Join(wd, d)
	}
	return filepath.Clean(d)
}

var stateNames = map[pb.AgentState]string{
	pb.AgentState_AGENT_STATE_STARTING:    "starting",
	pb.AgentState_AGENT_STATE_WORKING:     "working",
	pb.AgentState_AGENT_STATE_NEEDS_INPUT: "NEEDS INPUT",
	pb.AgentState_AGENT_STATE_IDLE:        "idle",
	pb.AgentState_AGENT_STATE_ERRORED:     "ERRORED",
	pb.AgentState_AGENT_STATE_EXITED:      "exited",
}

// browserGroup picks the Group for a new browser tab: the one named, else
// the one this command runs in (inside a Hammock pane), else the first.
func (w *world) browserGroup(ref string) (string, error) {
	if ref != "" {
		return w.resolve(ref, "group")
	}
	if p := w.panes[paneEnv("PANE_ID")]; p != nil {
		if t := w.tabs[p.TabId]; t != nil {
			return t.GroupId, nil
		}
	}
	if len(w.groups) == 0 {
		return "", fmt.Errorf("no Groups yet; create one with: hmk group new NAME")
	}
	return w.groups[0].Id, nil
}

func printWorld(w *world) {
	if len(w.groups) == 0 {
		fmt.Println("No Groups yet. Create one with: hmk group new NAME")
		return
	}
	for _, s := range w.groups {
		title := client.GroupTitle(s, w.groups, w.panes)
		if s.Icon != "" {
			title = s.Icon + " " + title
		}
		if on := w.computerName(s); on != "" {
			title += "  (on " + on + ")"
			if s.Offline {
				title += ", offline"
			}
		}
		printfSafe("%s  %s\n", s.Id, title)
		for _, tid := range s.TabIds {
			t := w.tabs[tid]
			if t == nil {
				continue
			}
			printfSafe("  %s  %s\n", t.Id, client.TabTitle(t, s))
			for _, pid := range tabPaneIDs(t) {
				p := w.panes[pid]
				if p == nil {
					continue
				}
				status := ""
				if f := floatOf(t, pid); f != nil {
					status = "  (floating)"
					if f.Minimised {
						status = "  (minimised)"
					}
				}
				if a := p.Agent; a != nil {
					// A program reporting its status (OSC 7501) goes by
					// its own name for itself.
					who := cmp.Or(a.Harness, a.Title, a.App, "program")
					status += fmt.Sprintf("  [%s %s", who, stateNames[a.State])
					if a.Progress != nil {
						status += fmt.Sprintf(" %d%%", *a.Progress)
					}
					status += "]"
					if a.Summary != "" {
						status += " " + a.Summary
					}
				}
				if p.Exited {
					status += fmt.Sprintf("  (exited %d)", p.ExitStatus)
				}
				focus := " "
				if t.FocusedPaneId == pid {
					focus = "*"
				}
				if p.Kind == pb.PaneKind_PANE_KIND_BROWSER {
					printfSafe("   %s%s  %s browser  %s%s\n", focus, p.Id, cell(p.Title, 24), p.Url, status)
					continue
				}
				if ed := p.GetEditor(); p.Kind == pb.PaneKind_PANE_KIND_EDITOR {
					if ed.GetDirty() {
						status += "  (unsaved)"
					}
					if ed.GetConflict() {
						status += "  (changed on disk)"
					}
					printfSafe("   %s%s  %s editor  %s%s\n", focus, p.Id, cell(p.Title, 24), ed.GetPath(), status)
					continue
				}
				if p.Kind == pb.PaneKind_PANE_KIND_DIFF {
					printfSafe("   %s%s  %s diff  %s%s\n", focus, p.Id, cell(p.Title, 24), p.GetDiff().GetRepo(), status)
					continue
				}
				if p.Kind == pb.PaneKind_PANE_KIND_SIMULATOR {
					printfSafe("   %s%s  %s simulator  %s%s\n", focus, p.Id, cell(p.Title, 24), p.DeviceUdid, status)
					continue
				}
				if src := p.GetLog(); kindJSON(p) == "log" {
					what := src.GetKind()
					if src.GetPath() != "" {
						what += "  " + src.GetPath()
					}
					printfSafe("   %s%s  %s log  %s%s\n", focus, p.Id, cell(p.Title, 24), what, status)
					continue
				}
				printfSafe("   %s%s  %s %dx%d  %s%s\n", focus, p.Id, cell(p.Title, 24), p.Cols, p.Rows, p.Cwd, status)
			}
		}
	}
	if len(w.grants) > 0 {
		fmt.Println("\nPermission requests (hmk grant allow|always|deny ID):")
		for _, g := range w.grants {
			printfSafe("  %s  %s\n", g.Id, grantText(g))
			if g.Description != "" && g.Headline != "" {
				printfSafe("      the agent says: %q\n", g.Description)
			}
			if g.Person {
				printfSafe("      %s: allow or deny it in the Hammock app or on your phone\n", g.Origin)
			}
		}
	}
	if len(w.attn) > 0 {
		fmt.Println("\nNeeds you:")
		for _, a := range w.attn {
			printfSafe("  %s  %s  %s\n", a.PaneId, stateNames[a.State], a.Summary)
		}
	}
}

// tabPaneIDs lists a tab's panes: its splits', then its floating ones.
func tabPaneIDs(t *pb.Tab) []string {
	ids := layoutPaneIDs(t.GetLayout())
	for _, f := range t.GetFloating() {
		ids = append(ids, f.PaneId)
	}
	return ids
}

// floatOf is pane id's place floating over t (nil: it's in the splits).
func floatOf(t *pb.Tab, id string) *pb.FloatingPane {
	for _, f := range t.GetFloating() {
		if f.PaneId == id {
			return f
		}
	}
	return nil
}

// parseFrame reads "X,Y,W,H" in fractions of a tab.
func parseFrame(s string) (*pb.Frame, error) {
	var v [4]float32
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("--at wants X,Y,W,H (fractions of the tab), not %q", s)
	}
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil || f < 0 || f > 1 {
			return nil, fmt.Errorf("--at wants X,Y,W,H between 0 and 1, not %q", s)
		}
		v[i] = float32(f)
	}
	return &pb.Frame{X: v[0], Y: v[1], W: v[2], H: v[3]}, nil
}

func layoutPaneIDs(l *pb.Layout) []string {
	if l == nil {
		return nil
	}
	if id := l.GetPaneId(); id != "" {
		return []string{id}
	}
	var out []string
	for _, c := range l.GetSplit().GetChildren() {
		out = append(out, layoutPaneIDs(c)...)
	}
	return out
}

// sendFlags reads `hmk send PANE`'s [TEXT] [--key NAME]... [--enter]. --key
// with no name after it is a mistake, not the text "--key".
func sendFlags(args []string) (text string, keys []string, enter bool, err error) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--enter":
			enter = true
		case a == "--key":
			if i+1 >= len(args) {
				return "", nil, false, fmt.Errorf("--key needs a key name, like --key ctrl-c")
			}
			keys = append(keys, args[i+1])
			i++
		case i == 0:
			text = a
		default:
			return "", nil, false, fmt.Errorf("usage: hmk send PANE [TEXT] [--key NAME]... [--enter]")
		}
	}
	return text, keys, enter, nil
}

// cell is s for a column n terminal cells wide: cut short with "…", or
// padded with spaces, by how wide it shows (a CJK character or an emoji
// takes two cells), not by its count of characters as fmt's %-24s does.
func cell(s string, n int) string {
	s = termSafe(s) // as printed, so a control character counts as the one cell its U+FFFD takes
	if ansi.StringWidth(s) > n {
		s = ansi.Truncate(s, n, "…")
	}
	return s + strings.Repeat(" ", max(0, n-ansi.StringWidth(s)))
}

// grantText is what a permission request asks: the daemon's headline (an
// older daemon's: the agent's description).
func grantText(g *pb.GrantRequest) string {
	if g.Headline != "" {
		return g.Headline
	}
	return g.RequesterPaneId + " wants to " + g.Description
}

// joinKeyWarning is what to beware of in a join request's key.
func joinKeyWarning(j *pb.JoinRequest, name string) string {
	switch {
	case j.NameHasOtherKey:
		return "  (" + name + " usually joins with a different key)"
	case j.NewKey:
		return "  (first time this key has asked to join)"
	}
	return ""
}

// grantFor is what an agent's refused message asks permission for: the
// action and the pane, as RequestGrant names them ("" if it can't ask).
func grantFor(m *pb.ClientMessage) (action, target string) {
	// DevTools for a page is as much as controlling the page: asked for as
	// that, whatever else the message needs (a refusal for that asks again).
	var spec *pb.PaneSpec
	switch x := m.Msg.(type) {
	case *pb.ClientMessage_CreatePane:
		spec = x.CreatePane.Spec
	case *pb.ClientMessage_CreateTab:
		spec = x.CreateTab.Pane
	case *pb.ClientMessage_ReplacePane:
		spec = x.ReplacePane.Spec
	}
	if spec != nil {
		if page, ok := daemon.DevToolsPage(spec.Url); ok {
			return "browse", page
		}
	}
	switch x := m.Msg.(type) {
	case *pb.ClientMessage_Input:
		return "send", x.Input.PaneId
	case *pb.ClientMessage_SignalPane:
		return "send", x.SignalPane.PaneId
	case *pb.ClientMessage_SwitchAgent:
		return "send", x.SwitchAgent.PaneId
	case *pb.ClientMessage_SleepPane:
		return "send", x.SleepPane.PaneId
	case *pb.ClientMessage_AgentSend:
		return "send", x.AgentSend.PaneId
	case *pb.ClientMessage_AgentAnswer:
		return "send", x.AgentAnswer.PaneId
	case *pb.ClientMessage_ClearLog:
		return "send", x.ClearLog.PaneId
	case *pb.ClientMessage_ClosePane:
		return "close", x.ClosePane.PaneId
	case *pb.ClientMessage_ReplacePane:
		return "close", x.ReplacePane.PaneId
	case *pb.ClientMessage_BrowserNavigate:
		return "browse", x.BrowserNavigate.PaneId
	case *pb.ClientMessage_ReadPane:
		return "read", x.ReadPane.PaneId
	case *pb.ClientMessage_ReadEditor:
		return "read", x.ReadEditor.PaneId
	case *pb.ClientMessage_EditorCommand:
		return "read", x.EditorCommand.PaneId // agents may only select
	case *pb.ClientMessage_CreatePane:
		return "create", x.CreatePane.SplitPaneId
	case *pb.ClientMessage_ListSessions, *pb.ClientMessage_SessionExcerpts:
		// Sessions from other folders: asked for the agent's own pane.
		return "sessions", paneEnv("PANE_ID")
	}
	return "", ""
}

// askAndRetry asks the person to allow an agent's refused message, waiting
// for their answer, and sends it again if they do.
func askAndRetry(c *client.Client, m *pb.ClientMessage, action, target string, refused error) (*pb.ServerMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fmt.Fprintln(os.Stderr, "hammock: asking the user for permission…")
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RequestGrant{RequestGrant: &pb.RequestGrant{
		TargetPaneId: target, Action: action, Description: "hammock " + strings.Join(os.Args[1:], " "),
	}}})
	if err != nil {
		return nil, refused
	}
	if d := r.GetGrantDecision(); d == nil || !d.Allowed {
		return nil, fmt.Errorf("the user didn't allow it")
	}
	return c.Call(ctx, m)
}

// dropFlag removes a flag and its value from args.
func dropFlag(args []string, name string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == name {
			i++
			continue
		}
		if strings.HasPrefix(args[i], name+"=") {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func filterGroups(gs []*pb.Group, keep func(*pb.Group) bool) []*pb.Group {
	var out []*pb.Group
	for _, g := range gs {
		if keep(g) {
			out = append(out, g)
		}
	}
	return out
}
