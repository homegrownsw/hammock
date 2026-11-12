package daemon

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// errDeferred tells the read loop not to reply yet; the handler arranged a
// later reply (grant requests wait for a human).
var errDeferred = errors.New("deferred")

// grantTimeout is how long a request waits for your answer (tests shorten it).
var grantTimeout = 2 * time.Minute

type pendingGrant struct {
	req     *pb.GrantRequest
	c       *conn
	replyTo uint64
	done    func(allowed bool) // instead of c: the daemon itself asked (sshagent.go); called holding d.mu
	timer   *time.Timer
	group   string
	// person: c (not an agent) asked to do something only you may
	// (approval.go): allowed once or not at all, answered only by the app
	// or a paired phone, and withdrawn if c goes.
	person bool
}

func grantKey(requester, action, group string) string { return requester + "|" + action + "|" + group }

// requestGrant handles an MCP server's request to act out of scope. Callers hold d.mu.
func (d *Daemon) requestGrant(c *conn, replyTo uint64, m *pb.RequestGrant) (*pb.ServerMessage, error) {
	// Asking about a pane that's gone (you closed it first) would only ask
	// you to allow nothing: the agent hears it's closed instead.
	if err := d.paneGone(m.TargetPaneId); err != nil {
		return nil, err
	}
	group := d.groupOfPane(m.RequesterPaneId)
	headline := d.grantHeadline(m.RequesterPaneId, m.Action, m.TargetPaneId)
	note := agentNote(m.Description)
	if d.remembered[grantKey(m.RequesterPaneId, m.Action, group)] && group != "" && group == d.groupOfPane(m.TargetPaneId) {
		d.audit("mcp:"+m.RequesterPaneId, "grant-auto", m.TargetPaneId, auditGrant(headline, note))
		return grantReply(true, "remembered for this Group"), nil
	}
	d.openGrant(&pendingGrant{
		req: &pb.GrantRequest{
			Id: newID("gr"), RequesterPaneId: m.RequesterPaneId, TargetPaneId: m.TargetPaneId,
			Action: m.Action, Description: note, Headline: headline, CreatedUnixMs: time.Now().UnixMilli(),
		},
		c: c, replyTo: replyTo, group: group,
	})
	return nil, errDeferred
}

// paneGone says a pane has closed: it's neither here nor on another
// computer. Callers hold d.mu.
func (d *Daemon) paneGone(target string) error {
	if target != "" && d.panes[target] == nil && d.routes[target] == nil {
		return fmt.Errorf("no such pane %q: it has closed", target)
	}
	return nil
}

// openGrant asks you: Needs You on every client (and your other
// computers'), and a push. It's denied if nobody answers in grantTimeout.
// Callers hold d.mu.
func (d *Daemon) openGrant(g *pendingGrant) {
	id := g.req.Id
	g.timer = time.AfterFunc(grantTimeout, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.resolveGrant(id, pb.RespondGrant_DECISION_DENY, "timed out")
	})
	d.grants[id] = g
	if g.person {
		d.audit("local", "approval-requested", g.req.TargetPaneId, g.req.Headline+" ("+g.req.Origin+")")
	} else {
		d.audit("mcp:"+g.req.RequesterPaneId, "grant-requested", g.req.TargetPaneId, auditGrant(g.req.Headline, g.req.Description))
	}
	d.publish(&pb.StateUpdate{Grants: []*pb.GrantRequest{proto.CloneOf(g.req)}})
	title, sub := d.pushTitle(g.req.RequesterPaneId)
	if g.person {
		title, sub = "Hammock", g.req.Origin
	}
	// Hammock's words only: the agent's own can say anything.
	d.push("needs_input", id, pushNote{Title: title, Subtitle: sub, Body: g.req.Headline, Category: "grant", Grant: id, Pane: g.req.RequesterPaneId},
		func() bool { return d.grants[id] != nil })
}

// resolveGrant answers a pending grant. Callers hold d.mu.
func (d *Daemon) resolveGrant(id string, decision pb.RespondGrant_Decision, reason string) {
	g, ok := d.grants[id]
	if !ok {
		return
	}
	delete(d.grants, id)
	g.timer.Stop()
	allowed := decision == pb.RespondGrant_DECISION_ALLOW_ONCE || decision == pb.RespondGrant_DECISION_ALLOW_GROUP
	if allowed && !g.person {
		d.noteOnceGrant(g.req) // the daemon lets the action through (caller.go)
	}
	if decision == pb.RespondGrant_DECISION_ALLOW_GROUP && g.group != "" && !g.person {
		d.remembered[grantKey(g.req.RequesterPaneId, g.req.Action, g.group)] = true
	}
	if reason == "" {
		reason = decision.String()
	}
	what := auditGrant(g.req.Headline, g.req.Description)
	if g.person {
		what = g.req.Headline + " (" + g.req.Origin + ")"
	}
	d.audit("user", "grant-"+map[bool]string{true: "allowed", false: "denied"}[allowed], g.req.TargetPaneId, what+" ("+reason+")")
	if g.done != nil {
		g.done(allowed)
	} else {
		msg := grantReply(allowed, reason)
		msg.ReplyTo = g.replyTo
		g.c.send(msg)
	}
	d.publish(&pb.StateUpdate{ResolvedGrantIds: []string{id}})
}

// dropGrantsFor denies a disconnected requester's pending grants. Callers hold d.mu.
func (d *Daemon) dropGrantsFor(c *conn) {
	for id, g := range d.grants {
		if g.c == c && (g.done == nil || g.person) {
			d.resolveGrant(id, pb.RespondGrant_DECISION_DENY, "requester disconnected")
		}
	}
}

func grantReply(allowed bool, reason string) *pb.ServerMessage {
	return &pb.ServerMessage{Msg: &pb.ServerMessage_GrantDecision{GrantDecision: &pb.GrantDecision{Allowed: allowed, Reason: reason}}}
}

func (d *Daemon) groupOfPane(id string) string {
	if p := d.panes[id]; p != nil {
		if t := d.tabs[p.tabID()]; t != nil {
			return t.GroupId
		}
	}
	return ""
}

// grantVerbs say what each grant action does to its target pane.
var grantVerbs = map[string]string{
	"read":      "read",
	"send":      "type into",
	"close":     "close",
	"create":    "open a pane next to",
	"browse":    "control the browser in",
	"simulator": "control the simulator in",
}

// grantHeadline is what a grant request asks, in Hammock's words, from the
// action and the panes involved: "Claude in Web ▸ api wants to type into
// Web ▸ api ▸ pane 2 (npm run dev)". The agent's own description can say
// anything, so clients show this as the request. Callers hold d.mu.
func (d *Daemon) grantHeadline(requester, action, target string) string {
	who := d.agentWho(requester)
	if action == "sessions" {
		return who + " wants to search and read your agent sessions from other folders"
	}
	verb := grantVerbs[action]
	if verb == "" {
		verb = "use (" + action + ")"
	}
	return fmt.Sprintf("%s wants to %s %s", who, verb, d.paneLabel(target))
}

// agentWho names the agent in a pane: "Claude in Web ▸ api". Callers hold d.mu.
func (d *Daemon) agentWho(paneID string) string {
	if paneID == outsidePane {
		return outsideWho
	}
	who := d.agentName(paneID)
	if d.panes[paneID] != nil {
		if path := d.tabPath(paneID); path != "" {
			who += " in " + path
		}
	}
	return who
}

// outsideWho names a program outside Hammock's panes that's scoped as an
// agent (person.go).
const outsideWho = "A program outside Hammock"

// agentName names the agent in a pane by its harness: "Claude", else "An
// agent". Callers hold d.mu.
func (d *Daemon) agentName(paneID string) string {
	if p := d.panes[paneID]; p != nil {
		if a := p.snapshotInfo().Agent; a != nil && a.Harness != "" {
			return capitalize(a.Harness)
		}
	}
	return "An agent"
}

// tabPath is "Group ▸ tab" for a pane. Callers hold d.mu.
func (d *Daemon) tabPath(paneID string) string {
	g, group, tab := d.tabParts(paneID)
	if g == nil {
		return tab
	}
	return group + " ▸ " + tab
}

// tabParts is where a pane is: its Group (nil if it has none) and the
// names tabPath joins. Callers hold d.mu.
func (d *Daemon) tabParts(paneID string) (g *pb.Group, group, tab string) {
	p := d.panes[paneID]
	if p == nil {
		return nil, "", ""
	}
	t := d.tabs[p.tabID()]
	if t == nil {
		return nil, "", ""
	}
	g = d.groups[t.GroupId]
	tab = client.TabTitle(t, g)
	if t.Name == "" {
		if info := p.snapshotInfo(); info.Kind == pb.PaneKind_PANE_KIND_BROWSER && info.Title != "" {
			tab = info.Title
		}
	}
	if g == nil {
		return nil, "", tab
	}
	group = g.Name
	if group == "" {
		group = "Group"
		if lp := d.panes[g.LastFocusedPaneId]; lp != nil {
			if cwd := lp.snapshotInfo().Cwd; cwd != "" {
				group = client.DirTitle(cwd)
			}
		}
	}
	return g, group, tab
}

// paneLabel names a pane by where it is: "Group ▸ tab ▸ pane 2 (title)",
// the position only when the tab has several panes, the title only in
// parentheses (a program sets it). Callers hold d.mu.
func (d *Daemon) paneLabel(paneID string) string {
	p := d.panes[paneID]
	if p == nil {
		return "a pane that's gone"
	}
	label := d.tabPath(paneID)
	if t := d.tabs[p.tabID()]; t != nil {
		if ids := tabPanes(t); len(ids) > 1 {
			label += fmt.Sprintf(" ▸ pane %d", slices.Index(ids, paneID)+1)
		}
	}
	if label == "" {
		label = "a pane"
	}
	if title := oneLine(p.snapshotInfo().Title, 60); title != "" {
		label += " (" + title + ")"
	}
	return label
}

// agentNote is an agent's description of its request, made safe to show:
// one paragraph of printable text, not too long.
func agentNote(s string) string {
	return oneLine(s, 500)
}

func auditGrant(headline, note string) string {
	if note == "" {
		return headline
	}
	return headline + ` — agent says: "` + note + `"`
}

// oneLine collapses s to a single line of printable text of at most n runes.
func oneLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n-1]) + "…"
	}
	return s
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}
