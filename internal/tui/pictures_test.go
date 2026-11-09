package tui

import (
	"strings"
	"testing"
)

// A picture goes in 4096-byte chunks (all but the last saying more come),
// then a virtual placement two cells wide; its cells carry its id.
func TestPictureCommands(t *testing.T) {
	p := pictures{next: 0x4b0001}
	id := p.send(make([]byte, 7000)) // 9336 bytes of base64: three chunks
	out := p.pending.String()
	if id != 0x4b0001 || strings.Count(out, "\x1b_G") != 4 {
		t.Fatalf("id %x, commands:\n%q", id, out)
	}
	if !strings.HasPrefix(out, "\x1b_Ga=t,f=100,t=d,i=4915201,q=2,m=1;") || !strings.Contains(out, "\x1b_Gm=1;") ||
		!strings.Contains(out, "\x1b_Gm=0;") || !strings.HasSuffix(out, "\x1b_Ga=p,U=1,i=4915201,c=2,r=1,q=2\x1b\\") {
		t.Fatalf("commands:\n%q", out)
	}
	c := placeholder(id, 1, RGB{})
	if c.FG != (RGB{0x4b, 0x00, 0x01}) || c.Ch != "\U0010EEEE̅̍" {
		t.Fatalf("cell %+v", c)
	}
}
