package schedtrig

import (
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Missing is what a trigger still needs before it can be saved, as a
// question ("Which label?"), "" when nothing: for an editor to show under
// one just added. The daemon checks again when it's saved.
func Missing(t *pb.EventTrigger) string {
	blank := func(s string) bool { return strings.TrimSpace(s) == "" }
	switch k := t.GetKind().(type) {
	case *pb.EventTrigger_Github:
		switch {
		case k.Github.Event == pb.GitHubTrigger_EVENT_LABELED && blank(k.Github.Label):
			return "Which label?"
		case k.Github.Event == pb.GitHubTrigger_EVENT_WORKFLOW && blank(k.Github.Workflow):
			return "Which workflow?"
		}
	case *pb.EventTrigger_Mac:
		if blank(k.Mac.Name) {
			switch {
			case k.Mac.Event == pb.MacTrigger_EVENT_APP_OPENED || k.Mac.Event == pb.MacTrigger_EVENT_APP_QUIT:
				return "Which app?"
			case IsPlace(k.Mac.Event):
				return "Which place?"
			}
		}
	case *pb.EventTrigger_Url:
		switch {
		case blank(k.Url.Url):
			return "Which URL should the check fetch?"
		case k.Url.On == pb.UrlCheckTrigger_ON_CONTAINS && blank(k.Url.Text):
			return "What text should the page contain?"
		}
	case *pb.EventTrigger_Agent:
		if !k.Agent.Finished && !k.Agent.NeedsYou {
			return "An agent trigger fires when it finishes, needs you, or both"
		}
	case *pb.EventTrigger_Log:
		switch {
		case k.Log.PaneId == "":
			return "Which log pane?"
		case blank(k.Log.Pattern):
			return "What should the log line match?"
		}
	case *pb.EventTrigger_Check:
		if blank(k.Check.Command) {
			return "Which command should the check run?"
		}
	case *pb.EventTrigger_Slack:
		if k.Slack.Event == pb.SlackTrigger_EVENT_MESSAGE && blank(k.Slack.Channel) {
			return "Which channel?"
		}
	}
	return ""
}

// MissingCondition is what an Only If condition still needs ("" when
// nothing).
func MissingCondition(c *pb.Condition) string {
	switch {
	case c.Kind == pb.Condition_KIND_WIFI && strings.TrimSpace(c.Wifi) == "":
		return "Which Wi-Fi network?"
	case c.Kind == pb.Condition_KIND_PLACE && strings.TrimSpace(c.Place) == "":
		return "Which place?"
	}
	return ""
}
