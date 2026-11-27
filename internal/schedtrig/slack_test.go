package schedtrig

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestSlackTriggers(t *testing.T) {
	for _, c := range []struct{ in, spec, words string }{
		{"slack:message=#Deploys", "slack:message=#deploys", "A Slack message in #deploys"},
		{"slack:message=C0123ABCD,from=@sam,mine", "slack:message=C0123ABCD,from=sam,mine", "A Slack message in C0123ABCD from “sam”, mine too"},
		{"slack:mention", "slack:mention", "I’m mentioned in Slack"},
		{"slack:mention,in=#ops,text=/deploy (failed|broke), again/", "slack:mention,in=#ops,text=/deploy (failed|broke), again/",
			"I’m mentioned in Slack (#ops) matching /deploy (failed|broke), again/"},
		{"slack:dm,from=Sam Example", "slack:dm,from=Sam Example", "A Slack direct message to me from “Sam Example”"},
		{"slack:reply,text=ship it", "slack:reply,text=ship it", "A reply in a Slack thread I started containing “ship it”"},
		{"slack:reaction=:eyes:", "slack:reaction=eyes", "I react with :eyes: in Slack"},
		{"slack:reaction,anyone,in=#ops", "slack:reaction,in=#ops,anyone", "Someone reacts to a Slack message (#ops)"},
	} {
		tr, err := Parse(c.in, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got := Spec(tr); got != c.spec {
			t.Errorf("%s: spec %q, want %q", c.in, got, c.spec)
		}
		if got := Describe(tr, nil); got != c.words {
			t.Errorf("%s: words %q, want %q", c.in, got, c.words)
		}
		if again, err := Parse(Spec(tr), nil); err != nil || Spec(again) != Spec(tr) {
			t.Errorf("%s: doesn't read back: %v", c.in, err)
		}
		if Kind(tr) != "slack" {
			t.Errorf("kind %q", Kind(tr))
		}
	}
	for _, bad := range []string{"slack:message", "slack:wave", "slack:dm,in=#x", "slack:mention=#x", "slack:message=#Bad Name", "slack:reply,text=/(/"} {
		if _, err := Parse(bad, nil); err == nil {
			t.Errorf("%s: should be refused", bad)
		}
	}
}

func TestSlackText(t *testing.T) {
	match, _ := SlackText("Deploy Failed")
	if !match("the deploy failed again") || match("deployed") {
		t.Fatal("contains")
	}
	re, _ := SlackText("/^ship (it|now)$/")
	if !re("Ship it") || re("ship later") {
		t.Fatal("regex")
	}
	if m, err := SlackText(""); m != nil || err != nil {
		t.Fatal("none")
	}
	if CheckSlack(&pb.SlackTrigger{Event: pb.SlackTrigger_EVENT_MESSAGE}) == nil {
		t.Fatal("a message trigger needs a channel")
	}
}
