package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestRemoteArgs(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want map[string]any
	}{
		{[]string{"select"}, map[string]any{"name": "select"}},
		{[]string{"down", "--repeat", "3"}, map[string]any{"name": "down", "repeat": 3}},
		{[]string{"home", "--hold", "1000"}, map[string]any{"name": "home", "holdms": 1000}},
		{[]string{"swipe", "left"}, map[string]any{"swipe": "left"}},
		{[]string{"swipe", "up", "long"}, map[string]any{"swipe": "up", "distance": "long"}},
		{[]string{}, nil},
		{[]string{"swipe"}, nil},
		{[]string{"up", "--repeat"}, nil},
		{[]string{"up", "--repeat", "lots"}, nil},
		{[]string{"up", "extra"}, nil},
	} {
		got, err := remoteArgs(c.in)
		if c.want == nil {
			if err == nil {
				t.Errorf("%q: want an error, got %v", c.in, got)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

func TestPickSimulatorDefaultIsNotAnEmulator(t *testing.T) {
	devs := []simDevice{
		{UDID: "AAAA", Name: "iPhone 17", Platform: "iOS"},
		{UDID: "avd:Pixel_9", Name: "Pixel 9", Platform: "Android", Booted: true},
	}
	if d, err := pickSimulator(devs, ""); err != nil || d.UDID != "AAAA" {
		t.Fatalf("default: %+v %v", d, err)
	}
	for _, ref := range []string{"avd:Pixel_9", "Pixel_9", "pixel 9"} {
		if d, err := pickSimulator(devs, ref); err != nil || d.UDID != "avd:Pixel_9" {
			t.Errorf("%q: %+v %v", ref, d, err)
		}
	}
	if _, err := pickSimulator(devs[1:], ""); err == nil {
		t.Error("only an AVD and none named: want an error")
	}
}

// hmk sim clipboard: no TEXT reads the device's clipboard; TEXT or - (stdin) sets it.
func TestSimClipboard(t *testing.T) {
	var sent []*pb.SimulatorCall
	call := func(m *pb.ClientMessage) (*pb.ServerMessage, error) {
		sent = append(sent, m.GetSimulatorCall())
		return &pb.ServerMessage{Msg: &pb.ServerMessage_SimulatorResult{SimulatorResult: &pb.SimulatorResult{Json: `{"text":"on the device"}`}}}, nil
	}
	var out bytes.Buffer
	if err := simClipboard(call, "p1", nil, nil, &out); err != nil || out.String() != "on the device" {
		t.Fatalf("get: %q %v", out.String(), err)
	}
	if err := simClipboard(call, "p1", []string{"hello", "there"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := simClipboard(call, "p1", []string{"-"}, strings.NewReader("from\nstdin"), &out); err != nil {
		t.Fatal(err)
	}
	want := []string{`get_clipboard `, `set_clipboard {"text":"hello there"}`, `set_clipboard {"text":"from\nstdin"}`}
	if len(sent) != len(want) {
		t.Fatalf("%d calls, want %d", len(sent), len(want))
	}
	for i, s := range sent {
		if got := s.Op + " " + s.ArgsJson; got != want[i] || s.PaneId != "p1" {
			t.Errorf("call %d: %s", i, got)
		}
	}
}
