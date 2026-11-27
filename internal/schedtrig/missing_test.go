package schedtrig

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestMissing(t *testing.T) {
	for spec, want := range map[string]string{
		"github:ci-failed":         "",
		"github:label=bug":         "",
		"mac:app-opened=Xcode":     "",
		"mac:wifi-joined":          "", // any network
		"place:arrive=Office":      "",
		"slack:mention":            "",
		"agent:finished":           "",
		"calendar:10,title=Stand":  "",
		"mac:disk<10%@/Volumes/Ex": "",
	} {
		tr, err := Parse(spec, nil)
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if got := Missing(tr); got != want {
			t.Errorf("%s: %q, want %q", spec, got, want)
		}
	}
	for _, c := range []struct {
		t    *pb.EventTrigger
		want string
	}{
		{&pb.EventTrigger{Kind: &pb.EventTrigger_Github{Github: &pb.GitHubTrigger{Event: pb.GitHubTrigger_EVENT_LABELED}}}, "Which label?"},
		{&pb.EventTrigger{Kind: &pb.EventTrigger_Github{Github: &pb.GitHubTrigger{Event: pb.GitHubTrigger_EVENT_WORKFLOW, Workflow: " "}}}, "Which workflow?"},
		{&pb.EventTrigger{Kind: &pb.EventTrigger_Mac{Mac: &pb.MacTrigger{Event: pb.MacTrigger_EVENT_APP_QUIT}}}, "Which app?"},
		{&pb.EventTrigger{Kind: &pb.EventTrigger_Mac{Mac: &pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLACE_LEAVE}}}, "Which place?"},
		{&pb.EventTrigger{Kind: &pb.EventTrigger_Url{Url: &pb.UrlCheckTrigger{}}}, "Which URL should the check fetch?"},
		{&pb.EventTrigger{Kind: &pb.EventTrigger_Url{Url: &pb.UrlCheckTrigger{Url: "https://example.com", On: pb.UrlCheckTrigger_ON_CONTAINS}}}, "What text should the page contain?"},
		{&pb.EventTrigger{Kind: &pb.EventTrigger_Log{Log: &pb.LogTrigger{Pattern: "x"}}}, "Which log pane?"},
		{&pb.EventTrigger{Kind: &pb.EventTrigger_Check{Check: &pb.CommandCheckTrigger{}}}, "Which command should the check run?"},
		{&pb.EventTrigger{Kind: &pb.EventTrigger_Slack{Slack: &pb.SlackTrigger{Event: pb.SlackTrigger_EVENT_MESSAGE}}}, "Which channel?"},
	} {
		if got := Missing(c.t); got != c.want {
			t.Errorf("%v: %q, want %q", c.t, got, c.want)
		}
	}
	if MissingCondition(&pb.Condition{Kind: pb.Condition_KIND_WIFI}) == "" || MissingCondition(&pb.Condition{Kind: pb.Condition_KIND_POWER}) != "" {
		t.Error("MissingCondition")
	}
}
