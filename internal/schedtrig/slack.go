package schedtrig

// Slack triggers (pb.SlackTrigger), written out:
//
//	slack:message=#CHANNEL   a message in a channel
//	slack:mention            a message that mentions me
//	slack:dm                 a direct message to me (or a group DM I'm in)
//	slack:reply              a reply in a thread I started
//	slack:reaction[=EMOJI]   I react to a message (with EMOJI)
//
// then any of, after commas:
//
//	in=#CHANNEL   only there (mention, reply, reaction)
//	from=NAME     only from someone (a reaction: by them); their name or id
//	mine          my own messages count too (message)
//	anyone        others' reactions count too (reaction)
//	text=TEXT     only messages containing TEXT, or matching /REGEX/:
//	              last, as it takes the rest (commas and all)

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const slackUsage = "slack:message=#CHANNEL, mention, dm, reply or reaction[=EMOJI], then ,in=#CHANNEL ,from=NAME ,mine ,anyone and ,text=TEXT|/REGEX/ (last)"

var slackEvents = map[string]pb.SlackTrigger_Event{
	"message": pb.SlackTrigger_EVENT_MESSAGE, "mention": pb.SlackTrigger_EVENT_MENTION, "dm": pb.SlackTrigger_EVENT_DM,
	"reply": pb.SlackTrigger_EVENT_THREAD_REPLY, "reaction": pb.SlackTrigger_EVENT_REACTION,
}

// SlackEventName is the word slack: reads for e ("reply").
func SlackEventName(e pb.SlackTrigger_Event) string {
	for k, v := range slackEvents {
		if v == e {
			return k
		}
	}
	return ""
}

// slackIDRE is a conversation's id, as Slack gives it (C…, G…, D…).
var (
	slackIDRE   = regexp.MustCompile(`^[CGD][A-Z0-9]{6,}$`)
	slackNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)
)

// SlackChannelIsID: c names a conversation by its id, not its name.
func SlackChannelIsID(c string) bool { return slackIDRE.MatchString(strings.TrimSpace(c)) }

// SlackChannel is a channel as a trigger keeps it: an id as it is, a name
// without its # and in lower case ("" for none).
func SlackChannel(c string) string {
	c = strings.TrimSpace(c)
	if SlackChannelIsID(c) {
		return c
	}
	return strings.ToLower(strings.TrimPrefix(c, "#"))
}

// SlackChannelWords is a channel for words: "#deploys", or its id.
func SlackChannelWords(c string) string {
	if c == "" || SlackChannelIsID(c) {
		return c
	}
	return "#" + strings.TrimPrefix(c, "#")
}

// SlackEmoji is an emoji's name, without colons or a skin tone.
func SlackEmoji(e string) string {
	e = strings.Trim(strings.TrimSpace(e), ":")
	if i := strings.Index(e, "::skin-tone"); i >= 0 {
		e = e[:i]
	}
	return strings.ToLower(e)
}

// SlackText is a Slack trigger's text filter as a test: containing it (any
// case), or matching it written /REGEX/ (any case, unless it has
// capitals). nil: it has none.
func SlackText(text string) (func(string) bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	if len(text) > 2 && strings.HasPrefix(text, "/") && strings.HasSuffix(text, "/") {
		re, err := CompileLog(text[1 : len(text)-1])
		if err != nil {
			return nil, err
		}
		return re.MatchString, nil
	}
	low := strings.ToLower(text)
	return func(s string) bool { return strings.Contains(strings.ToLower(s), low) }, nil
}

// parseSlack reads what follows slack: (rest).
func parseSlack(spec, rest string) (*pb.SlackTrigger, error) {
	text := ""
	if i := strings.Index(rest, ",text="); i >= 0 {
		rest, text = rest[:i], rest[i+len(",text="):]
	}
	parts := strings.Split(rest, ",")
	word, val, _ := strings.Cut(strings.TrimSpace(parts[0]), "=")
	word = strings.ToLower(strings.TrimSpace(word))
	switch word {
	case "messages", "channel":
		word = "message"
	case "mentioned", "mentions":
		word = "mention"
	case "direct-message", "im", "dms":
		word = "dm"
	case "thread", "thread-reply", "replies":
		word = "reply"
	case "react", "reactions", "reacted":
		word = "reaction"
	}
	e, ok := slackEvents[word]
	if !ok {
		return nil, fmt.Errorf("%q: %s", spec, slackUsage)
	}
	s := &pb.SlackTrigger{Event: e}
	val = strings.TrimSpace(val)
	switch e {
	case pb.SlackTrigger_EVENT_MESSAGE:
		s.Channel = SlackChannel(val)
	case pb.SlackTrigger_EVENT_REACTION:
		s.Emoji = SlackEmoji(val)
	default:
		if val != "" {
			return nil, fmt.Errorf("%q: slack:%s takes nothing after it (in=#CHANNEL for a channel)", spec, word)
		}
	}
	for _, p := range parts[1:] {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		v = strings.TrimSpace(v)
		switch strings.ToLower(k) {
		case "in", "channel":
			if e == pb.SlackTrigger_EVENT_DM {
				return nil, fmt.Errorf("%q: a direct message isn't in a channel", spec)
			}
			s.Channel = SlackChannel(v)
		case "from", "by":
			s.From = strings.TrimPrefix(v, "@")
		case "mine":
			s.Mine = true
		case "anyone", "others":
			s.Anyone = true
		case "emoji":
			s.Emoji = SlackEmoji(v)
		case "":
		default:
			return nil, fmt.Errorf("%q: %s", spec, slackUsage)
		}
	}
	s.Text = strings.TrimSpace(text)
	if err := CheckSlack(s); err != nil {
		return nil, err
	}
	return s, nil
}

// MaxSlackText is the longest a Slack trigger's text filter may be.
const MaxSlackText = 300

// CheckSlack says what's wrong with a Slack trigger, if anything.
func CheckSlack(s *pb.SlackTrigger) error {
	switch s.Event {
	case pb.SlackTrigger_EVENT_MESSAGE:
		if s.Channel == "" {
			return errors.New("which channel? slack:message=#CHANNEL")
		}
	case pb.SlackTrigger_EVENT_MENTION, pb.SlackTrigger_EVENT_DM, pb.SlackTrigger_EVENT_THREAD_REPLY, pb.SlackTrigger_EVENT_REACTION:
	default:
		return errors.New("an unknown Slack trigger (from a newer Hammock?)")
	}
	if c := s.Channel; c != "" && !SlackChannelIsID(c) && !slackNameRE.MatchString(c) {
		return fmt.Errorf("%q isn't a Slack channel's name", c)
	}
	if len([]rune(s.Text)) > MaxSlackText {
		return fmt.Errorf("the text is too long (at most %d characters)", MaxSlackText)
	}
	if _, err := SlackText(s.Text); err != nil {
		return err
	}
	return nil
}

// DescribeSlack says what fires a Slack trigger, in words.
func DescribeSlack(s *pb.SlackTrigger) string {
	in := ""
	if s.Channel != "" {
		in = " (" + SlackChannelWords(s.Channel) + ")"
	}
	var out string
	switch s.Event {
	case pb.SlackTrigger_EVENT_MESSAGE:
		out, in = "A Slack message in "+SlackChannelWords(s.Channel), ""
	case pb.SlackTrigger_EVENT_MENTION:
		out = "I’m mentioned in Slack"
	case pb.SlackTrigger_EVENT_DM:
		out, in = "A Slack direct message to me", ""
	case pb.SlackTrigger_EVENT_THREAD_REPLY:
		out = "A reply in a Slack thread I started"
	case pb.SlackTrigger_EVENT_REACTION:
		who := "I react"
		if s.Anyone {
			who = "Someone reacts"
		}
		if s.Emoji != "" {
			out = who + " with :" + s.Emoji + ": in Slack"
		} else {
			out = who + " to a Slack message"
		}
	default:
		return "Something in Slack"
	}
	out += in
	if s.From != "" {
		if s.Event == pb.SlackTrigger_EVENT_REACTION {
			out += " by “" + s.From + "”"
		} else {
			out += " from “" + s.From + "”"
		}
	}
	if s.Text != "" {
		if strings.HasPrefix(s.Text, "/") && strings.HasSuffix(s.Text, "/") && len(s.Text) > 2 {
			out += " matching " + s.Text
		} else {
			out += " containing “" + s.Text + "”"
		}
	}
	if s.Mine && s.Event == pb.SlackTrigger_EVENT_MESSAGE {
		out += ", mine too"
	}
	return out
}

// specSlack writes a Slack trigger as parseSlack reads it.
func specSlack(s *pb.SlackTrigger) string {
	out := "slack:" + SlackEventName(s.Event)
	switch {
	case s.Event == pb.SlackTrigger_EVENT_MESSAGE:
		out += "=" + SlackChannelWords(s.Channel)
	case s.Event == pb.SlackTrigger_EVENT_REACTION && s.Emoji != "":
		out += "=" + s.Emoji
	}
	if s.Channel != "" && s.Event != pb.SlackTrigger_EVENT_MESSAGE {
		out += ",in=" + SlackChannelWords(s.Channel)
	}
	if s.From != "" {
		out += ",from=" + s.From
	}
	if s.Mine {
		out += ",mine"
	}
	if s.Anyone {
		out += ",anyone"
	}
	if s.Text != "" {
		out += ",text=" + s.Text
	}
	return out
}
