package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Screen sharing is off until Settings turns it on; then this Mac's own
// clients get a display's video, and input only once they're watching it.
func TestScreenMedia(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	d, c, enc, _ := mediaDaemon(t)
	d.media.screenSource = func() frameSource { return enc }
	open := &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{Screen: true, InBand: true}}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, open); err == nil || !strings.Contains(err.Error(), "Screen Sharing is off") {
		t.Fatalf("screen while off: %v", err)
	}
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListDisplays{ListDisplays: &pb.ListDisplays{}}}).GetDisplays()
	if r.Enabled || r.Unavailable == "" {
		t.Fatalf("displays while off: %+v", r)
	}
	input := &pb.ClientMessage{Msg: &pb.ClientMessage_ScreenInput{ScreenInput: &pb.ScreenInput{Op: "mouse", ArgsJson: `{"type":"move","x":0.5,"y":0.5}`}}}
	if _, err := c.Call(ctx, input); err == nil {
		t.Fatal("input while off")
	}

	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("screen-sharing = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(ctx, input); err == nil || !strings.Contains(err.Error(), "open the screen") {
		t.Fatalf("input before watching: %v", err)
	}
	sid := call(t, c, open).GetMediaTicket().SessionId
	for n := 0; n < 10; n++ {
		f := waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetMediaFrame() != nil }).GetMediaFrame()
		if f.SessionId != sid || (n == 0 && !f.Keyframe) {
			t.Fatalf("frame %d: session %s key %v", n, f.SessionId, f.Keyframe)
		}
	}
	d.mu.Lock()
	var audited bool
	events, _ := d.store.RecentAudit(50)
	for _, e := range events {
		audited = audited || e.Action == "screen-share"
	}
	d.mu.Unlock()
	if !audited {
		t.Fatal("no audit entry for the screen session")
	}
	// Watching now: input goes on to the host (which isn't running here).
	if _, err := c.Call(ctx, input); err != nil && strings.Contains(err.Error(), "open the screen") {
		t.Fatalf("input while watching: %v", err)
	}
}

// The real screen host (bin/Hammock Screen.app, just screen), against a
// scratch daemon: HMK_SCREEN_LIVE=1 go test -run TestScreenHostLive. It
// lists this Mac's displays and, if Hammock Screen Sharing already has Screen
// Recording, watches the main one for a second. It never posts input.
func TestScreenHostLive(t *testing.T) {
	if os.Getenv("HMK_SCREEN_LIVE") != "1" {
		t.Skip("HMK_SCREEN_LIVE=1 to run against the real screen host")
	}
	root, _ := filepath.Abs("../..")
	t.Setenv("HMK_SCREEN_HOST", filepath.Join(root, "bin", "Hammock Screen.app", "Contents", "MacOS", "Hammock Screen"))
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("screen-sharing = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, c, _, _ := mediaDaemon(t)
	d.screens.path = screenHostPath()
	t.Cleanup(func() { _ = d.screens.send(map[string]any{"t": "quit"}) })
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListDisplays{ListDisplays: &pb.ListDisplays{}}}).GetDisplays()
	if len(r.Displays) == 0 {
		t.Fatalf("no displays: %+v", r)
	}
	for _, x := range r.Displays {
		t.Logf("display %d %q %dx%d @%gx main=%v", x.Id, x.Name, x.Width, x.Height, x.Scale, x.Main)
	}
	t.Logf("screen recording %v, accessibility %v, unavailable %q", r.ScreenRecording, r.Accessibility, r.Unavailable)
	if !r.ScreenRecording {
		t.Skip("Hammock Screen Sharing has no Screen Recording yet: not watching")
	}
	sid := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{Screen: true, InBand: true, MaxPixels: 1920 * 1200}}}).GetMediaTicket().SessionId
	start := time.Now()
	var n, bytes int
	for time.Since(start) < 2*time.Second {
		f := waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetMediaFrame() != nil }).GetMediaFrame()
		if n == 0 {
			t.Logf("first frame after %v: %dx%d key=%v %d bytes", time.Since(start), f.Width, f.Height, f.Keyframe, len(f.Data))
		}
		n++
		bytes += len(f.Data)
		c.Send(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_MediaAck{MediaAck: &pb.MediaAck{SessionId: sid, Seqs: []uint32{f.Seq}}}})
	}
	t.Logf("%d frames, %d KB in 2 s", n, bytes/1024)
}

// sharpEncoder is a fakeEncoder that takes the sharp tier (codecSource),
// marking its frames HEVC while it's on.
type sharpEncoder struct {
	*fakeEncoder
	mu     sync.Mutex
	codecs []string // asked for, in order
	sizes  []int    // sizes asked for, in order
}

func (e *sharpEncoder) encodeWith(paneID, sid string, bitrate, fps, maxPixels int, codec string) {
	e.mu.Lock()
	e.codecs = append(e.codecs, codec)
	e.mu.Unlock()
	e.fakeEncoder.startEncode(paneID, sid, bitrate, fps, maxPixels)
}

func (e *sharpEncoder) setCodec(sid, codec string) {
	e.mu.Lock()
	e.codecs = append(e.codecs, codec)
	e.mu.Unlock()
}

func (e *sharpEncoder) asked() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.codecs)
}

// A screen starts in the sharp tier when the viewer decodes it (else
// H.264), and drops to H.264 when the link can't carry it.
func TestScreenTiers(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("screen-sharing = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, c, enc, _ := mediaDaemon(t)
	sharp := &sharpEncoder{fakeEncoder: enc}
	d.media.screenSource = func() frameSource { return sharp }

	plain := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{Screen: true, InBand: true}}}).GetMediaTicket().SessionId
	if got := sharp.asked(); !slices.Equal(got, []string{"h264"}) {
		t.Fatalf("a viewer without HEVC got %v", got)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseMedia{CloseMedia: &pb.CloseMedia{SessionId: plain}}})

	sid := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{Screen: true, InBand: true, Codecs: []string{"hevc444"}}}}).GetMediaTicket().SessionId
	if got := sharp.asked(); !slices.Equal(got, []string{"h264", "hevc444"}) {
		t.Fatalf("a viewer with HEVC got %v", got)
	}
	// The link can't carry it: down to H.264, once.
	d.media.mu.Lock()
	s := d.media.byID[sid]
	d.media.mu.Unlock()
	s.mu.Lock()
	s.rate = mediaTierDown / 2
	s.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for !slices.Equal(sharp.asked(), []string{"h264", "hevc444", "h264"}) {
		if time.Now().After(deadline) {
			t.Fatalf("no tier change: %v", sharp.asked())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (e *sharpEncoder) setView(sid string, x, y, w, h, aspect float32) {}

func (e *sharpEncoder) setSize(sid string, maxPixels int) {
	e.mu.Lock()
	e.sizes = append(e.sizes, maxPixels)
	e.mu.Unlock()
}

func (e *sharpEncoder) sized() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.sizes)
}

// A screen's size follows its rate: down to 720p on a slow link at once,
// and up again only after a while.
func TestScreenSizeFollowsRate(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("screen-sharing = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, c, enc, _ := mediaDaemon(t)
	sharp := &sharpEncoder{fakeEncoder: enc}
	d.media.screenSource = func() frameSource { return sharp }
	sid := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{Screen: true, InBand: true, MaxPixels: 3072 * 1728}}}).GetMediaTicket().SessionId
	d.media.mu.Lock()
	s := d.media.byID[sid]
	d.media.mu.Unlock()
	s.mu.Lock()
	s.rate, s.floor = 1_000_000, 300_000
	s.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for !slices.Contains(sharp.sized(), 1280*720) {
		if time.Now().After(deadline) {
			t.Fatalf("no smaller size on a slow link: %v", sharp.sized())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The pointer goes to each viewer of its display, the shape's image only
// the first time.
func TestScreenCursor(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("screen-sharing = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, c, enc, _ := mediaDaemon(t)
	d.media.screenSource = func() frameSource { return enc }
	sid := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{Screen: true, InBand: true}}}).GetMediaTicket().SessionId
	shape := &pb.ScreenCursor{ShapeId: 7, Png: []byte("png"), HotX: 3, HotY: 4, Width: 24, Height: 24}
	for i := 0; i < 2; i++ {
		d.media.screenCursor(screenMsg{Display: 5, IsMain: true, X: 0.25, Y: 0.5, Visible: true, Shape: 7}, shape)
		got := waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetScreenCursor() != nil }).GetScreenCursor()
		if got.SessionId != sid || got.X != 0.25 || got.ShapeId != 7 || got.HotX != 3 {
			t.Fatalf("pointer %d: %+v", i, got)
		}
		if (i == 0) != (len(got.Png) > 0) {
			t.Fatalf("pointer %d: image sent %v", i, len(got.Png) > 0)
		}
	}
}

// The menu bar's view of the server lists screen viewers, and ends them.
func TestServerStatusScreenViewers(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("screen-sharing = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, c, enc, _ := mediaDaemon(t)
	d.media.screenSource = func() frameSource { return enc }
	sid := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{Screen: true, InBand: true}}}).GetMediaTicket().SessionId
	st := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ServerStatus{ServerStatus: &pb.ServerStatus{}}}).GetServerStatusReply()
	if st.Version == "" || st.Panes == 0 || !st.ScreenSharing || len(st.ScreenViewers) != 1 || st.ScreenViewers[0].SessionId != sid {
		t.Fatalf("status: %+v", st)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_EndScreenSession{EndScreenSession: &pb.EndScreenSession{All: true}}})
	ended := waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetMediaEnded() != nil }).GetMediaEnded()
	if ended.SessionId != sid {
		t.Fatalf("ended %+v", ended)
	}
	if st := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ServerStatus{ServerStatus: &pb.ServerStatus{}}}).GetServerStatusReply(); len(st.ScreenViewers) != 0 {
		t.Fatalf("still viewers: %+v", st.ScreenViewers)
	}
}
