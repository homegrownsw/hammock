package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alexjsp/hammock/internal/daemon"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// errNotRunning makes hmk status exit 3 (as systemctl status does) when the
// daemon isn't running, without printing an error: that's the answer.
var errNotRunning = errors.New("hmkd isn't running")

// runStatus is hmk status [--json]: whether the daemon is running, and
// what a server's person (or the Unraid plugin's page) wants at a glance.
func runStatus(args []string) error {
	asJSON := hasFlag(args, "--json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type network struct {
		ID        string   `json:"id"`
		Label     string   `json:"label,omitempty"`
		Chosen    bool     `json:"chosen"`
		Connected bool     `json:"connected"`
		Addresses []string `json:"addresses,omitempty"`
		Public    bool     `json:"public,omitempty"`
	}
	type status struct {
		Running        bool      `json:"running"`
		Dir            string    `json:"dir"`
		Socket         string    `json:"socket"`
		Version        string    `json:"version,omitempty"`
		Name           string    `json:"name,omitempty"`
		Groups         int       `json:"groups"`
		Tabs           int       `json:"tabs"`
		Panes          int       `json:"panes"`
		Agents         int       `json:"agents"`
		NeedsYou       int       `json:"needs_you"`
		RemoteEnabled  bool      `json:"remote_enabled"`
		Networks       []network `json:"networks"`
		ExtraAddresses []string  `json:"extra_addresses,omitempty"`
		Computers      int       `json:"linked_computers"`
		Online         int       `json:"linked_online"`
		Devices        int       `json:"paired_devices"`
		LinkRequests   int       `json:"link_requests"`
	}
	st := status{Dir: daemon.DefaultDir(), Socket: socketPath(), Networks: []network{}}
	c, err := connect(ctx)
	if err != nil {
		if asJSON {
			if err := printJSON(st); err != nil {
				return err
			}
		} else {
			fmt.Printf("hmkd isn't running (no daemon at %s). Start it: hmk start\n", st.Socket)
		}
		return errNotRunning
	}
	defer c.Close()
	w, err := fetchWorld(ctx, c)
	if err != nil {
		return err
	}
	st.Running = true
	if h := c.Welcome; h != nil {
		st.Version, st.Name = h.DaemonVersion, h.HostName
	}
	if w.computers != nil {
		for _, lc := range w.computers.List {
			if lc.ThisComputer {
				st.Name = lc.Name
				continue
			}
			st.Computers++
			if lc.Online {
				st.Online++
			}
		}
	}
	// This computer's own: not Groups shared with it, or a linked one's.
	own := map[string]bool{}
	for _, g := range w.groups {
		if g.HostUserId == "" {
			own[g.Id] = true
		}
	}
	st.Groups = len(own)
	for _, p := range w.panes {
		t := w.tabs[p.TabId]
		if t == nil || !own[t.GroupId] {
			continue
		}
		st.Panes++
		if p.Agent != nil && p.Agent.Harness != "" {
			st.Agents++
		}
	}
	for _, t := range w.tabs {
		if own[t.GroupId] {
			st.Tabs++
		}
	}
	st.NeedsYou = len(w.attn)
	for _, r := range w.linkReqs {
		if r.Code != "" && !r.AcceptedHere {
			st.LinkRequests++
		}
	}
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ListDevices{ListDevices: &pb.ListDevices{}}})
	if err == nil {
		d := r.GetDevices()
		st.RemoteEnabled = d.RemoteEnabled
		st.Devices = len(d.Devices)
		st.ExtraAddresses = d.ExtraAddresses
		for _, n := range d.Networks {
			st.Networks = append(st.Networks, network{n.Id, n.Label, n.Enabled, n.Connected, n.Addresses, n.Public})
		}
	}
	if asJSON {
		return printJSON(st)
	}
	printfSafe("hmkd %s is running as %s (%s)\n", st.Version, st.Name, st.Dir)
	fmt.Printf("  %d Groups, %d tabs, %d panes, %d agents", st.Groups, st.Tabs, st.Panes, st.Agents)
	if st.NeedsYou > 0 {
		fmt.Printf(", %d need you", st.NeedsYou)
	}
	fmt.Println()
	remote := "off (hmk remote on)"
	if st.RemoteEnabled {
		remote = "on"
		for _, n := range st.Networks {
			if n.Chosen && n.Connected {
				remote += fmt.Sprintf(", %s %v", n.ID, n.Addresses)
			}
		}
	}
	fmt.Printf("  remote access %s\n", remote)
	fmt.Printf("  %d linked computers (%d online), %d paired devices\n", st.Computers, st.Online, st.Devices)
	if st.LinkRequests > 0 {
		fmt.Printf("  %d asking to link: hmk link pending\n", st.LinkRequests)
	}
	return nil
}
