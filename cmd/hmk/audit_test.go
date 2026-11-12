package main

import "testing"

func TestAuditCount(t *testing.T) {
	for _, c := range []struct {
		args []string
		want int
		ok   bool
	}{
		{nil, 30, true},
		{[]string{"5"}, 5, true},
		{[]string{"-5"}, 0, false},
		{[]string{"0"}, 0, false},
		{[]string{"lots"}, 0, false},
	} {
		n, err := auditCount(c.args)
		if (err == nil) != c.ok || n != c.want {
			t.Errorf("auditCount(%q) = %d, %v; want %d, ok %v", c.args, n, err, c.want, c.ok)
		}
	}
}
