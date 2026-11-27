package daemon

// An automation saved on another of your computers (hmk --on, MCP's
// computer_id, the Mac's editor) goes to its daemon, which may be an
// older Hammock's: one that doesn't know a newer trigger, or Only If,
// would drop what it doesn't know and save the rest (Only If gone, it
// would run whenever). So each computer says what it can do
// (Computer.features), and one that doesn't say it can is refused such an
// automation, here, before it's sent.

import (
	"fmt"
	"slices"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// featureAutomations2: This Mac triggers, Only If, calendar end and free
// time, and the newer GitHub events.
const featureAutomations2 = "automations-2"

// featureSessions: automations that carry on a session (Schedule.session).
const featureSessions = "automation-sessions"

// features are what this computer's Hammock can do that an older one
// can't (Computer.features).
var features = []string{featureAutomations2, featureSessions}

// needsAutomations2 says what of s an older Hammock wouldn't know ("" if
// nothing).
func needsAutomations2(s *pb.Schedule) string {
	if len(s.GetOnlyIf()) > 0 {
		return "Only If"
	}
	for _, t := range s.GetEvents() {
		switch k := t.Kind.(type) {
		case *pb.EventTrigger_Mac:
			return "This Mac triggers"
		case *pb.EventTrigger_Calendar:
			if k.Calendar.When != pb.CalendarTrigger_WHEN_START {
				return "calendar end and free time triggers"
			}
		case *pb.EventTrigger_Github:
			if k.Github.Event > pb.GitHubTrigger_EVENT_RELEASE {
				return "these GitHub triggers"
			}
		}
	}
	return ""
}

// peerCanSave refuses s for the linked computer uid when it uses what
// that computer's Hammock is too old for. Callers hold d.mu.
func (d *Daemon) peerCanSave(uid string, s *pb.Schedule) error {
	l := d.comp.links[uid]
	if l == nil {
		return nil
	}
	has := l.GetRecord().GetFeatures()
	what := ""
	switch {
	case !slices.Contains(has, featureAutomations2) && needsAutomations2(s) != "":
		what = needsAutomations2(s)
	case !slices.Contains(has, featureSessions) && s.GetSession() != pb.Schedule_SESSION_NEW:
		what = "automations that carry on a session"
	default:
		return nil
	}
	name := strings.TrimSuffix(cmpOr(l.GetRecord().GetName(), "That computer"), ".local")
	return fmt.Errorf("%s has an older Hammock, which doesn't know %s: update Hammock there first", name, what)
}
