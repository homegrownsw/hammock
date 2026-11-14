package main

import (
	"reflect"
	"testing"
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
