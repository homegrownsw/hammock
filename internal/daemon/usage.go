package daemon

// Agents' subscription usage limits, for the sidebar (off by default, per
// agent, in the apps), as each agent's reader in internal/agents finds them
// on disk, and, with usage-live = true in ~/.config/hammock/config
// (Settings ▸ Agents), as each agent's own service gives them (its
// LiveUsage, with the sign-in its CLI keeps here: read for that request
// only and never kept). Only the agents in usage-live-agents are asked
// (Settings keeps it to the agents shown in the sidebar; unset, every
// one); claude-usage-live, the setting's name when it was Claude's alone,
// asks only Anthropic. Each service is asked every few minutes; whichever
// of its answer and what's on disk is newer is shown. A failed request
// (signed out, an expired token) drops the service's answer once it's
// liveStale old, so what's on disk, if anything, shows again; a throttled
// one keeps it until the service may be asked again.

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

const usageEvery = 30 * time.Second

// liveEvery is how often each agent's service is asked for its usage:
// Anthropic's throttles (429) when asked much more often.
const liveEvery = 5 * time.Minute

// liveStale is how old a service's last answer may get, while asking it
// fails, before it stops being shown.
const liveStale = 3 * liveEvery

// usageReader reads each agent's limits; each agent's own reader remembers
// what it last read, so unchanged files aren't read again.
type usageReader struct {
	readers []agentUsage
	// liveFor says whether an agent's service is to be asked (usageLive; a
	// test's own).
	liveFor func(id string) bool
}

// agentUsage is one agent's readers: from disk, and from its service.
type agentUsage struct {
	id       string
	read     func() *pb.AgentUsage // nil: nothing on disk
	fetch    func(context.Context) (*pb.AgentUsage, time.Duration, error)
	live     *pb.AgentUsage // the service's last good answer
	liveNext time.Time      // when to ask it again
}

// usageLive: agent id's service is to be asked for its usage: usage-live
// is on and id is in usage-live-agents (when that's set), or id is Claude
// and claude-usage-live (the setting's name when it was Claude's alone)
// is on.
func usageLive(id string) bool {
	if hammockconfig.Bool("usage-live", false) {
		list, set := hammockconfig.Value("usage-live-agents")
		if !set {
			return true
		}
		return slices.Contains(strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' }), id)
	}
	return id == "claude" && hammockconfig.Bool("claude-usage-live", false)
}

// watchUsage looks at the agents' usage every so often until the daemon
// stops, telling clients when it changes.
func (d *Daemon) watchUsage() {
	var r usageReader
	t := time.NewTicker(usageEvery)
	defer t.Stop()
	for {
		u := r.read()
		d.mu.Lock()
		if !proto.Equal(u, d.usage) {
			d.usage = u
			d.publish(&pb.StateUpdate{Usage: proto.CloneOf(u)})
		}
		d.mu.Unlock()
		select {
		case <-d.done:
			return
		case <-t.C:
		}
	}
}

func (r *usageReader) read() *pb.UsageLimits {
	if r.readers == nil {
		for _, a := range agents.All {
			if a.Usage == nil && a.LiveUsage == nil {
				continue
			}
			u := agentUsage{id: a.ID, fetch: a.LiveUsage}
			if a.Usage != nil {
				u.read = a.Usage()
			}
			r.readers = append(r.readers, u)
		}
	}
	on := usageLive
	if r.liveFor != nil {
		on = r.liveFor
	}
	now := time.Now()
	// Ask the services that are due at once: each may take a while.
	var wg sync.WaitGroup
	for i := range r.readers {
		if u := &r.readers[i]; u.due(u.fetch != nil && on(u.id), now) {
			wg.Go(func() { u.askLive(now) })
		}
	}
	wg.Wait()
	out := &pb.UsageLimits{}
	for i := range r.readers {
		u := &r.readers[i]
		var a *pb.AgentUsage
		if u.read != nil {
			a = u.read()
		}
		if a = newerUsage(a, u.live); a != nil {
			out.Agents = append(out.Agents, a)
		}
	}
	return out
}

// due: the agent's service is to be asked now (at most every liveEvery,
// while live). Off, it forgets the last answer.
func (u *agentUsage) due(live bool, now time.Time) bool {
	if !live || u.fetch == nil {
		u.live, u.liveNext = nil, time.Time{}
		return false
	}
	return !now.Before(u.liveNext)
}

// askLive asks the agent's service. After a failed request the last good
// answer is kept while it's under liveStale old, or while throttled.
func (u *agentUsage) askLive(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, wait, err := u.fetch(ctx)
	switch {
	case err == nil && a != nil:
		a.Harness = u.id
		u.live = a
	case wait == 0 && u.live != nil && now.Sub(time.UnixMilli(u.live.FetchedUnixMs)) >= liveStale:
		u.live = nil
	}
	u.liveNext = now.Add(max(wait, liveEvery))
}

// newerUsage is whichever of a and b was fetched later (either may be nil).
func newerUsage(a, b *pb.AgentUsage) *pb.AgentUsage {
	if a == nil || (b != nil && b.FetchedUnixMs > a.FetchedUnixMs) {
		return b
	}
	return a
}
