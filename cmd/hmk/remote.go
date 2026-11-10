package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const remoteUsage = "usage: hmk remote on|off | networks | enable|disable NETWORK | address add|remove HOST[:PORT]"

// runRemote is hmk remote: turning remote access on or off, and choosing
// the networks it listens on (ADR-0016).
func runRemote(call func(*pb.ClientMessage) (*pb.ServerMessage, error), args []string) error {
	if len(args) == 0 {
		args = []string{"networks"}
	}
	devices := func() (*pb.Devices, error) {
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ListDevices{ListDevices: &pb.ListDevices{}}})
		if err != nil {
			return nil, err
		}
		return r.GetDevices(), nil
	}
	set := func(d *pb.Devices, nets, extra []string) error {
		_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetRemoteNetworks{SetRemoteNetworks: &pb.SetRemoteNetworks{Networks: nets, ExtraAddresses: extra}}})
		return err
	}
	chosen := func(d *pb.Devices) []string {
		var out []string
		for _, n := range d.Networks {
			if n.Enabled {
				out = append(out, n.Id)
			}
		}
		return out
	}
	switch args[0] {
	case "on", "off":
		_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetRemoteAccess{SetRemoteAccess: &pb.SetRemoteAccess{Enabled: args[0] == "on"}}})
		return err

	case "networks", "status":
		d, err := devices()
		if err != nil {
			return err
		}
		if slices.Contains(args, "--json") {
			type network struct {
				ID        string   `json:"id"`
				Label     string   `json:"label"`
				Chosen    bool     `json:"chosen"`
				Connected bool     `json:"connected"`
				Addresses []string `json:"addresses"`
				Public    bool     `json:"public"`
			}
			out := struct {
				Enabled        bool      `json:"enabled"`
				Networks       []network `json:"networks"`
				ExtraAddresses []string  `json:"extra_addresses"`
			}{d.RemoteEnabled, []network{}, append([]string{}, d.ExtraAddresses...)}
			for _, n := range d.Networks {
				out.Networks = append(out.Networks, network{n.Id, n.Label, n.Enabled, n.Connected, append([]string{}, n.Addresses...), n.Public})
			}
			return printJSON(out)
		}
		state := "off"
		if d.RemoteEnabled {
			state = "on"
		}
		fmt.Printf("Remote access is %s. Networks (* listened on whenever this computer is on it):\n", state)
		for _, n := range d.Networks {
			mark := " "
			if n.Enabled {
				mark = "*"
			}
			where := strings.Join(n.Addresses, ", ")
			if !n.Connected {
				where = "not on it now"
			}
			label := n.Label
			if label == n.Id {
				label = ""
			}
			note := ""
			if n.Public {
				note = "  (a public address: anyone who can reach it could try to connect)"
			}
			printfSafe("  %s %-18s %-10s %s%s\n", mark, n.Id, label, where, note)
		}
		if len(d.ExtraAddresses) > 0 {
			printfSafe("Extra addresses: %s\n", strings.Join(d.ExtraAddresses, ", "))
		}
		fmt.Println("Change with: hmk remote enable|disable NETWORK, hmk remote address add|remove HOST[:PORT]")
		return nil

	case "enable", "disable":
		if len(args) != 2 {
			return errors.New(remoteUsage)
		}
		d, err := devices()
		if err != nil {
			return err
		}
		id := args[1]
		// A network's name works too, when only one has it ("Wi-Fi").
		var named []string
		for _, n := range d.Networks {
			if strings.EqualFold(n.Label, id) {
				named = append(named, n.Id)
			}
		}
		if len(named) == 1 {
			id = named[0]
		}
		nets := chosen(d)
		if args[0] == "enable" {
			if !slices.Contains(nets, id) {
				nets = append(nets, id)
			}
		} else {
			if !slices.Contains(nets, id) {
				return fmt.Errorf("%s isn't chosen (hmk remote networks lists them)", id)
			}
			nets = slices.DeleteFunc(nets, func(x string) bool { return x == id })
		}
		return set(d, nets, d.ExtraAddresses)

	case "address", "addresses":
		if len(args) != 3 || (args[1] != "add" && args[1] != "remove") {
			return errors.New(remoteUsage)
		}
		d, err := devices()
		if err != nil {
			return err
		}
		extra := slices.Clone(d.ExtraAddresses)
		if args[1] == "add" {
			if !slices.Contains(extra, args[2]) {
				extra = append(extra, args[2])
			}
		} else {
			if !slices.Contains(extra, args[2]) {
				return fmt.Errorf("%s isn't one (hmk remote networks lists them)", args[2])
			}
			extra = slices.DeleteFunc(extra, func(x string) bool { return x == args[2] })
		}
		return set(d, chosen(d), extra)
	}
	return errors.New(remoteUsage)
}
