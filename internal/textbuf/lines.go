package textbuf

// Offset is the position of 1-based line:column (column in UTF-16 units,
// 1-based; 0 is the start of the line), clamped to the document.
func (t Text) Offset(line, col int) int {
	if line <= 1 {
		line = 1
	}
	pos, l := 0, 1
	for l < line {
		i := pos
		for i < len(t) && t[i] != '\n' {
			i++
		}
		if i == len(t) {
			return len(t) // past the last line: its end
		}
		pos, l = i+1, l+1
	}
	end := pos
	for end < len(t) && t[end] != '\n' {
		end++
	}
	if col > 1 {
		pos += col - 1
	}
	return min(pos, end)
}

// LineCol is the 1-based line and column of pos.
func (t Text) LineCol(pos int) (line, col int) {
	pos = min(max(pos, 0), len(t))
	line, start := 1, 0
	for i := 0; i < pos; i++ {
		if t[i] == '\n' {
			line, start = line+1, i+1
		}
	}
	return line, pos - start + 1
}

// Lines splits s into lines (without their "\n").
func Lines(t Text) []string {
	var out []string
	start := 0
	for i, c := range t {
		if c == '\n' {
			out = append(out, t[start:i].String())
			start = i + 1
		}
	}
	return append(out, t[start:].String())
}

// LineChange is a run of lines that differ: old lines [OldFrom, OldTo) are
// new lines [NewFrom, NewTo) (0-based).
type LineChange struct {
	OldFrom, OldTo, NewFrom, NewTo int
}

// DiffLines compares two files line by line (Myers' algorithm), giving the
// runs that differ in order. Past maxD edits it gives up and reports the
// whole middle as changed.
func DiffLines(a, b []string) []LineChange {
	// Trim what they start and end with in common: most edits are small.
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	a2, b2 := a[p:len(a)-s], b[p:len(b)-s]
	if len(a2) == 0 && len(b2) == 0 {
		return nil
	}
	if len(a2) == 0 || len(b2) == 0 {
		return []LineChange{{p, p + len(a2), p, p + len(b2)}}
	}
	ops, ok := myers(a2, b2, 2000)
	if !ok {
		return []LineChange{{p, p + len(a2), p, p + len(b2)}}
	}
	// ops: for each step, 0 same, 1 delete (a), 2 insert (b).
	var out []LineChange
	i, j := 0, 0
	var cur *LineChange
	for _, op := range ops {
		switch op {
		case 0:
			if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
			i++
			j++
		case 1:
			if cur == nil {
				cur = &LineChange{p + i, p + i, p + j, p + j}
			}
			i++
			cur.OldTo = p + i
		case 2:
			if cur == nil {
				cur = &LineChange{p + i, p + i, p + j, p + j}
			}
			j++
			cur.NewTo = p + j
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

// myers finds the shortest edit script from a to b, as ops (0 same, 1
// delete, 2 insert); false past maxD edits. It keeps, for each d, only the
// diagonals d could reach (memory goes as d², not d times the files' length).
func myers(a, b []string, maxD int) ([]byte, bool) {
	n, m := len(a), len(b)
	off := n + m + 1
	v := make([]int, 2*off+1)
	var trace [][]int // trace[d]: v[off-d-1 … off+d+1] before step d
	for d := 0; d <= n+m; d++ {
		if d > maxD {
			return nil, false
		}
		trace = append(trace, append([]int(nil), v[off-d-1:off+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, d), true
			}
		}
	}
	return nil, false
}

func backtrack(trace [][]int, a, b []string, dEnd int) []byte {
	x, y := len(a), len(b)
	var ops []byte
	for d := dEnd; d > 0; d-- {
		snap := trace[d]
		at := func(k int) int { return snap[k+d+1] } // v[off+k] before step d
		k := x - y
		var pk int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := at(pk)
		py := px - pk
		for x > px && y > py {
			ops = append(ops, 0)
			x--
			y--
		}
		if x == px {
			ops = append(ops, 2)
		} else {
			ops = append(ops, 1)
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		ops = append(ops, 0)
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}
