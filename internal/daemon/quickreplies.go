package daemon

import (
	"errors"
	"strings"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// defaultQuickReplies are offered while the quick-reply key is unset (or
// set to nothing): keys.go says so.
var defaultQuickReplies = []string{"Continue", "Looks good", "Run the tests"}

// maxQuickReplies and maxQuickReplyLen bound what a phone may write into
// the config file: a handful of short lines, each one line.
const (
	maxQuickReplies  = 20
	maxQuickReplyLen = 200
)

// quickReplies answers a QuickReplyCommand: the quick replies the Apple
// Watch offers (the quick-reply key), replaced first when it sets them.
// Who may send it: your own phone or app (the gates refuse agents, guests
// and your other computers).
func quickReplies(m *pb.QuickReplyCommand) (*pb.ServerMessage, error) {
	if m.Set {
		var lines []string
		for _, r := range m.Replies {
			r = strings.Join(strings.Fields(r), " ") // one line, as the file holds it
			if r == "" {
				continue
			}
			if len(r) > maxQuickReplyLen {
				return nil, errors.New("a quick reply is too long")
			}
			lines = append(lines, r)
		}
		if len(lines) > maxQuickReplies {
			return nil, errors.New("too many quick replies")
		}
		var err error
		if len(lines) == 0 {
			err = hammockconfig.Unset("quick-reply")
		} else {
			err = hammockconfig.SetRepeated("quick-reply", lines)
		}
		if err != nil {
			return nil, err
		}
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_QuickReplies{QuickReplies: &pb.QuickReplies{Replies: currentQuickReplies()}}}, nil
}

// currentQuickReplies is the quick-reply key's lines, else the defaults.
func currentQuickReplies() []string {
	vals, _ := hammockconfig.Repeated("quick-reply")
	if len(vals) == 0 {
		return defaultQuickReplies
	}
	return vals
}
