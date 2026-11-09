package tui

import (
	"slices"
	"testing"
)

// A collapsed Group keeping several tabs: it narrows toward them,
// and with fewer rows than kept tabs the selected one comes first.
func TestNarrowTo(t *testing.T) {
	cases := []struct {
		n, rows int
		kept    []int
		sel     int
		want    []int
	}{
		{6, 2, []int{1, 4}, 1, []int{1, 4}},       // settled: just the kept tabs
		{6, 4, []int{1, 4}, 1, []int{0, 1, 2, 4}}, // closing: neighbours of kept tabs, nearest first
		{6, 1, []int{1, 4}, 4, []int{4}},          // an agent's tab sliding in: selected first
		{6, 3, []int{0, 5}, -1, []int{0, 1, 5}},   // nothing selected here
		{3, 5, []int{1}, -1, []int{0, 1, 2}},      // more rows than tabs
	}
	for _, c := range cases {
		if got := narrowTo(c.n, c.rows, c.kept, c.sel); !slices.Equal(got, c.want) {
			t.Errorf("narrowTo(%d, %d, %v, %d) = %v, want %v", c.n, c.rows, c.kept, c.sel, got, c.want)
		}
	}
}
