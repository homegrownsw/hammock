package agentview

import (
	"encoding/json"
	"regexp"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// Shell is a command the agent runs in the background, and the file Claude
// Code writes its output to.
type Shell struct {
	*pb.AgentShell
	Output string
}

var (
	shellOutput = regexp.MustCompile(`Output is being written to: (\S+\.output)`)
	taskID      = regexp.MustCompile(`(?s)<task-id>(.*?)</task-id>`)
)

// Shells is the session's background shells, oldest first (copies).
func (c *Claude) Shells() []Shell {
	out := make([]Shell, len(c.shells))
	for i, s := range c.shells {
		out[i] = Shell{proto.CloneOf(s.AgentShell), s.Output}
	}
	return out
}

// startShell: a Bash call's result that says it's running in the
// background (it was asked to, or you sent it there).
func (c *Claude) startShell(blk *pb.AgentBlock, out string, structured json.RawMessage, ms int64) {
	var r struct {
		ID string `json:"backgroundTaskId"`
	}
	if json.Unmarshal(structured, &r) != nil || r.ID == "" {
		return
	}
	m := shellOutput.FindStringSubmatch(out)
	if m == nil {
		return
	}
	desc := blk.Title
	if strings.HasPrefix(desc, "Ran `") {
		desc = ""
	}
	if i, ok := c.shellAt[r.ID]; ok {
		c.shells[i].Output = m[1]
		return
	}
	c.shellAt[r.ID] = len(c.shells)
	c.shells = append(c.shells, &Shell{&pb.AgentShell{Id: r.ID, Command: blk.Input, Description: desc, StartedUnixMs: ms}, m[1]})
}

// endShell: a task notification for one of its shells, or a call that
// stopped one.
func (c *Claude) endShell(id, status, summary string) {
	i, ok := c.shellAt[id]
	if !ok {
		return
	}
	s := c.shells[i]
	s.Finished = true
	if summary != "" {
		s.Summary = summary
	}
	if status == "failed" || status == "killed" {
		s.Failed = true
	}
}

// stoppedShell: KillShell or TaskStop, by the id it was given.
func (c *Claude) stoppedShell(input string) {
	var in struct {
		ShellID string `json:"shell_id"`
		TaskID  string `json:"task_id"`
	}
	if json.Unmarshal([]byte(input), &in) == nil {
		c.endShell(in.ShellID+in.TaskID, "killed", "Stopped")
	}
}
