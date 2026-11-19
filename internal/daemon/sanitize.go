package daemon

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Text from another computer (a host's Groups, a guest's or a computer's
// name) is shown in your apps, terminal and notifications: it arrives
// without control characters (terminal escapes among them) or
// bidirectional overrides (which make text read other than it is), and
// cut to a sensible length.

// cleanText is s without C0 or C1 controls, DEL or bidi controls, at most n
// runes. With breaks, line breaks and tabs stay; without, they're spaces.
func cleanText(s string, n int, breaks bool) string {
	ok := func(r rune) bool {
		if breaks && (r == '\n' || r == '\t') {
			return true
		}
		return !unicode.IsControl(r) && !unicode.Is(unicode.Bidi_Control, r) && r != utf8.RuneError
	}
	clean := true
	for _, r := range s {
		if !ok(r) {
			clean = false
			break
		}
	}
	if !clean {
		s = strings.Map(func(r rune) rune {
			switch {
			case ok(r):
				return r
			case r == '\n' || r == '\t' || r == '\r':
				return ' '
			}
			return -1
		}, s)
	}
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n])
	}
	return s
}

// cleanLine is cleanText on one line.
func cleanLine(s string, n int) string { return cleanText(s, n, false) }

// Lengths: names (people, computers, Groups, tabs); titles and summaries;
// paths and URLs.
const (
	nameMax  = 128
	titleMax = 512
	pathMax  = 4096
)

// cleanHostState tidies what a host sent of its Groups before it's kept
// and shown here.
func cleanHostState(u *pb.StateUpdate) {
	for _, g := range u.Groups {
		g.Name, g.Folder = cleanLine(g.Name, nameMax), cleanLine(g.Folder, pathMax)
		if g.Icon = cleanLine(g.Icon, nameMax); !validIcon(g.Icon) {
			g.Icon = "" // an emoji (or a symbol's name), nothing else
		}
	}
	for _, t := range u.Tabs {
		t.Name, t.AutoName = cleanLine(t.Name, nameMax), cleanLine(t.AutoName, nameMax)
	}
	for _, p := range u.Panes {
		p.Title, p.Cwd = cleanLine(p.Title, titleMax), cleanLine(p.Cwd, pathMax)
		p.Url, p.FaviconUrl = cleanLine(p.Url, pathMax), cleanLine(p.FaviconUrl, pathMax)
		for i := range p.Command {
			p.Command[i] = cleanLine(p.Command[i], pathMax)
		}
		for i := range p.Processes {
			p.Processes[i] = cleanLine(p.Processes[i], titleMax)
		}
		for i := range p.Program {
			p.Program[i] = cleanLine(p.Program[i], pathMax)
		}
	}
	for _, a := range u.Attention {
		a.Summary = cleanText(a.Summary, pathMax, true)
	}
	for _, p := range u.People {
		cleanPerson(p)
	}
}

// cleanPerson tidies a person's name and device name, as sent by them or a host.
func cleanPerson(p *pb.Person) {
	if p == nil {
		return
	}
	p.Name, p.DeviceName = cleanLine(strings.TrimSpace(p.Name), nameMax), cleanLine(strings.TrimSpace(p.DeviceName), nameMax)
}

// cleanComputer tidies what a computer says of itself.
func cleanComputer(r *pb.Computer) {
	if r == nil {
		return
	}
	r.Name, r.Model = cleanLine(strings.TrimSpace(r.Name), nameMax), cleanLine(r.Model, nameMax)
	r.Kind, r.Os = cleanLine(r.Kind, nameMax), cleanLine(r.Os, nameMax)
	r.DefaultAgent = cleanLine(r.DefaultAgent, nameMax)
}

// withValidUTF8 is a copy of m whose strings are all UTF-8 (what isn't is
// replaced by U+FFFD), for a message protobuf refused to marshal: one bad
// string mustn't keep a client from its whole state.
func withValidUTF8(m proto.Message) proto.Message {
	m = proto.Clone(m)
	fixUTF8(m.ProtoReflect())
	return m
}

func fixUTF8(m protoreflect.Message) {
	fix := func(v protoreflect.Value) protoreflect.Value {
		return protoreflect.ValueOfString(strings.ToValidUTF8(v.String(), "�"))
	}
	type set struct {
		fd protoreflect.FieldDescriptor
		v  protoreflect.Value
	}
	var sets []set // after Range: a message mustn't change while it's ranged over
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		str, msg := fd.Kind() == protoreflect.StringKind, fd.Message() != nil
		switch {
		case fd.IsMap():
			mp := v.Map()
			keys, vals := fd.MapKey().Kind() == protoreflect.StringKind, fd.MapValue()
			var bad []protoreflect.MapKey
			mp.Range(func(k protoreflect.MapKey, mv protoreflect.Value) bool {
				switch {
				case keys && !utf8.ValidString(k.String()):
					bad = append(bad, k)
				case vals.Kind() == protoreflect.StringKind:
					mp.Set(k, fix(mv))
				case vals.Message() != nil:
					fixUTF8(mv.Message())
				}
				return true
			})
			for _, k := range bad {
				mv := mp.Get(k)
				mp.Clear(k)
				if vals.Kind() == protoreflect.StringKind {
					mv = fix(mv)
				} else if vals.Message() != nil {
					fixUTF8(mv.Message())
				}
				mp.Set(fix(k.Value()).MapKey(), mv)
			}
		case fd.IsList():
			l := v.List()
			for i := range l.Len() {
				if str {
					l.Set(i, fix(l.Get(i)))
				} else if msg {
					fixUTF8(l.Get(i).Message())
				}
			}
		case str && !utf8.ValidString(v.String()):
			sets = append(sets, set{fd, fix(v)})
		case msg:
			fixUTF8(v.Message())
		}
		return true
	})
	for _, s := range sets {
		m.Set(s.fd, s.v)
	}
}
