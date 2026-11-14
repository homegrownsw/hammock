package main

import "testing"

func TestReportSequence(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"working", "Syncing photos"}, "\x1b]7501;state=working:msg=U3luY2luZyBwaG90b3M=\x1b\\"},
		{[]string{"blocked", "--kind", "permission", "--app=terraform", "--id", "eu-west/db", "--progress", "40"},
			"\x1b]7501;state=blocked:id=eu-west/db:kind=permission:progress=40:app=terraform\x1b\\"},
		{[]string{"done", "--title", "US East"}, "\x1b]7501;state=done:title=VVMgRWFzdA==\x1b\\"},
		{[]string{"clear", "--id", "us-east"}, "\x1b]7501;state=clear:id=us-east\x1b\\"},
		{[]string{"clear"}, "\x1b]7501;state=clear\x1b\\"},
	} {
		got, err := reportSequence(c.args)
		if err != nil || got != c.want {
			t.Errorf("%q: %q, %v; want %q", c.args, got, err, c.want)
		}
	}
	for _, bad := range [][]string{
		{}, {"busy"}, {"working", "two\nlines"}, {"working", "--progress", "101"}, {"done", "--progress", "5"},
		{"working", "--kind", "auth"}, {"blocked", "--kind", "money"}, {"working", "--id", "a//b"},
		{"working", "--app", "has space"}, {"clear", "--app", "x"}, {"working", "--nope"}, {"working", "a", "b"},
	} {
		if _, err := reportSequence(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}
