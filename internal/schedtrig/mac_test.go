package schedtrig

import (
	"slices"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The newer triggers read as written, in words, and back again.
func TestParseNewerTriggers(t *testing.T) {
	for spec, want := range map[string]string{
		"mac:disk<10%":                    "The startup disk has less than 10% free",
		"mac:disk<50GB@/Volumes/Backup":   "/Volumes/Backup has less than 50 GB free",
		"mac:disk":                        "The startup disk has less than 10% free",
		"mac:battery<20%":                 "The battery falls below 20%",
		"mac:plugged-in":                  "The Mac is plugged in",
		"mac:unplugged":                   "The Mac is unplugged",
		"mac:wake":                        "The Mac wakes",
		"mac:wake=30":                     "The Mac wakes after sleeping 30 minutes or more",
		"mac:away=15":                     "I’ve been away from the Mac for 15 minutes",
		"mac:back":                        "I’m back at the Mac after 10 minutes or more away",
		"mac:drive":                       "A drive is connected",
		"mac:drive=Backup":                "A drive named “Backup” is connected",
		"mac:wifi-joined=Office":          "The Mac joins the Wi-Fi “Office”",
		"mac:wifi-left":                   "The Mac leaves a Wi-Fi network",
		"mac:app-opened=Xcode":            "“Xcode” opens",
		"mac:app-quit=com.apple.dt.Xcode": "“com.apple.dt.Xcode” quits",
		"mac:display":                     "A display is connected",
		"calendar:end,title=Standup":      "At the end of events titled “Standup”",
		"calendar:start":                  "At the start of events",
		"calendar:free":                   "Free time of 30 minutes or more begins in working hours",
		"calendar:free=60,calendar=Work":  "Free time of 60 minutes or more begins in working hours (Work)",
		"github:issue-opened":             "A new issue is opened",
		"github:approved@acme/widgets":    "My pull request is approved (acme/widgets)",
		"github:workflow=Deploy":          "The “Deploy” workflow finishes",
		"github:workflow=ci.yml@acme/w":   "The “ci.yml” workflow finishes (acme/w)",
		"github:notification":             "I get a GitHub notification",
		"place:arrive=Office":             "The Mac arrives at “Office”",
		"place:leave=Studio":              "The Mac leaves “Studio”",
	} {
		tr, err := Parse(spec, nil)
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if got := Describe(tr, nil); got != want {
			t.Errorf("%s: %q, want %q", spec, got, want)
		}
		again, err := Parse(Spec(tr), nil)
		if err != nil || Spec(again) != Spec(tr) {
			t.Errorf("%s: written as %q, read back as %q (%v)", spec, Spec(tr), Spec(again), err)
		}
	}
	for _, bad := range []string{"mac:disk<0%", "mac:battery<100%", "mac:app-opened", "mac:away=0", "mac:nope", "mac:plugged-in=x", "github:workflow", "calendar:free=0", "place:arrive", "place:visit=Office", "url:https://example.com"} {
		if _, err := Parse(bad, nil); err == nil {
			t.Errorf("%s was taken", bad)
		}
	}
}

func TestConditions(t *testing.T) {
	for spec, want := range map[string]string{
		"power":             "the Mac is plugged in",
		"away":              "I’m away from the Mac",
		"away=10":           "I’ve been away for 10 minutes",
		"wifi=Home Network": "on the Wi-Fi “Home Network”",
		"hours=09:00-17:30": "between 09:00 and 17:30",
		"hours=22:00-06:00": "between 22:00 and 06:00",
		"days=mon-fri":      "on weekdays",
		"days=sat,sun":      "on weekends",
		"days=mon,wed":      "on Mon, Wed",
		"days=fri-mon":      "on Mon, Fri, Sat, Sun",
		"free":              "I’m not in a meeting",
		"disk>20%":          "the startup disk has more than 20% free",
		"place=Office":      "the Mac is at “Office”",
	} {
		c, err := ParseCondition(spec)
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if got := DescribeCondition(c); got != want {
			t.Errorf("%s: %q, want %q", spec, got, want)
		}
		again, err := ParseCondition(SpecCondition(c))
		if err != nil || SpecCondition(again) != SpecCondition(c) {
			t.Errorf("%s: written as %q, read back as %q (%v)", spec, SpecCondition(c), SpecCondition(again), err)
		}
	}
	for _, bad := range []string{"wifi", "hours=9-5", "hours=09:00-09:00", "days=daily", "disk>0%", "sunny", "place"} {
		if _, err := ParseCondition(bad); err == nil {
			t.Errorf("%s was taken", bad)
		}
	}
	if !InHours(23*60, 22*60, 6*60) || InHours(12*60, 22*60, 6*60) || !InHours(9*60, 9*60, 17*60) || InHours(17*60, 9*60, 17*60) {
		t.Error("InHours")
	}
	if d, _ := ParseDays("weekdays"); !slices.Equal(d, []int32{1, 2, 3, 4, 5}) {
		t.Errorf("weekdays: %v", d)
	}
	if w := OnlyIfWords([]*pb.Condition{{Kind: pb.Condition_KIND_POWER}, {Kind: pb.Condition_KIND_FREE}}); w != "the Mac is plugged in and I’m not in a meeting" {
		t.Errorf("OnlyIfWords: %q", w)
	}
}

// URL checks' words, options and URLs.
func TestURLCheckWords(t *testing.T) {
	u := &pb.UrlCheckTrigger{Url: "https://example.com/status?x=1", On: pb.UrlCheckTrigger_ON_CONTAINS, Text: "all good", EveryMinutes: 60}
	if got := DescribeURL(u); got != "https://example.com/status?x=1 starts containing “all good”, checked every hour" {
		t.Errorf("words: %q", got)
	}
	if got := URLFlags(u); got != "--url 'https://example.com/status?x=1' --url-every 60 --url-on contains --url-text 'all good'" {
		t.Errorf("flags: %q", got)
	}
	if got := URLTimeout(&pb.UrlCheckTrigger{EveryMinutes: 1, TimeoutSeconds: 300}); got.Seconds() != 60 {
		t.Errorf("timeout over the interval: %v", got)
	}
	for _, bad := range []string{"", "example.com", "ftp://example.com", "https://a:b@example.com", "https://"} {
		if CheckURL(bad) == nil {
			t.Errorf("%q was taken", bad)
		}
	}
	if err := CheckURL("http://localhost:8080/health"); err != nil {
		t.Error(err)
	}
}
