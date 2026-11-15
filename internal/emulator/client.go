package emulator

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/homegrownsw/hammock/internal/emulator/emupb"
)

// The controller's methods, called by name (emupb has its messages only).
const service = "/android.emulation.control.EmulatorController/"

// Client is a connection to one emulator's gRPC controller.
type Client struct {
	conn  *grpc.ClientConn
	token string
}

// tokenAuth sends the emulator's token with every call.
type tokenAuth string

func (t tokenAuth) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(t)}, nil
}
func (tokenAuth) RequireTransportSecurity() bool { return false } // localhost

// Dial connects to a running emulator, by its token.
func Dial(r Running) (*Client, error) {
	if !r.Reachable() {
		return nil, fmt.Errorf("the emulator for %s isn't reachable: it was started without -grpc-use-token", r.AVD)
	}
	conn, err := grpc.NewClient(fmt.Sprintf("localhost:%d", r.GRPCPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(tokenAuth(r.Token)),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<20)))
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, token: r.Token}, nil
}

// Close ends the connection.
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) call(ctx context.Context, method string, in, out proto.Message) error {
	if out == nil {
		out = &emptypb.Empty{}
	}
	return c.conn.Invoke(ctx, service+method, in, out)
}

// Status says whether it has booted, and its screen.
func (c *Client) Status(ctx context.Context) (*emupb.EmulatorStatus, error) {
	var st emupb.EmulatorStatus
	return &st, c.call(ctx, "getStatus", &emptypb.Empty{}, &st)
}

// Frames are an emulator's screen, written into shared memory: a file the
// emulator maps and writes each frame into (RGBA, row by row), with a
// message for each.
type Frames struct {
	Path string // the file (file:// for the emulator)
	Mem  []byte // mapped here, read-only
	W, H int    // its size in pixels, as the emulator last wrote it
	f    *os.File
}

// NewFrames makes a frame file at path big enough for w×h pixels.
func NewFrames(path string, w, h int) (*Frames, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	// Room for either orientation's frame.
	size := w * h * 4
	if err := f.Truncate(int64(size)); err != nil {
		f.Close()
		return nil, err
	}
	mem, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Frames{Path: path, Mem: mem, W: w, H: h, f: f}, nil
}

// Close unmaps and removes the file.
func (fr *Frames) Close() {
	_ = syscall.Munmap(fr.Mem)
	fr.f.Close()
	os.Remove(fr.Path)
}

// StreamFrames asks for the screen's frames into fr until ctx ends, calling
// frame for each (its size in pixels and the emulator's sequence number).
// Frames come only when the screen changes.
func (c *Client) StreamFrames(ctx context.Context, fr *Frames, frame func(w, h int, seq uint32)) error {
	s, err := c.conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, service+"streamScreenshot")
	if err != nil {
		return err
	}
	// An explicit size: asked for its own (0×0) into shared memory, the
	// emulator crashes. It fits the screen within this, keeping its shape.
	req := &emupb.ImageFormat{Format: emupb.ImageFormat_RGBA8888, Width: uint32(fr.W), Height: uint32(fr.H),
		Transport: &emupb.ImageTransport{Channel: emupb.ImageTransport_MMAP, Handle: "file://" + fr.Path}}
	if err := s.SendMsg(req); err != nil {
		return err
	}
	if err := s.CloseSend(); err != nil {
		return err
	}
	for {
		var img emupb.Image
		if err := s.RecvMsg(&img); err != nil {
			return err
		}
		w, h := int(img.GetFormat().GetWidth()), int(img.GetFormat().GetHeight())
		if w*h*4 > len(fr.Mem) {
			continue // bigger than the file (the caller makes a bigger one)
		}
		frame(w, h, img.GetSeq())
	}
}

// Screenshot is the screen as a PNG.
func (c *Client) Screenshot(ctx context.Context) ([]byte, int, int, error) {
	var img emupb.Image
	if err := c.call(ctx, "getScreenshot", &emupb.ImageFormat{Format: emupb.ImageFormat_PNG}, &img); err != nil {
		return nil, 0, 0, err
	}
	return img.GetImage(), int(img.GetFormat().GetWidth()), int(img.GetFormat().GetHeight()), nil
}

// Touch is one finger, in the screen's pixels: pressure 0 lifts it.
type Touch struct {
	ID, X, Y, Pressure int
}

// Touches sends where each finger is now (several: a pinch).
func (c *Client) Touches(ctx context.Context, ts ...Touch) error {
	ev := &emupb.TouchEvent{}
	for _, t := range ts {
		ev.Touches = append(ev.Touches, &emupb.Touch{X: int32(t.X), Y: int32(t.Y), Identifier: int32(t.ID), Pressure: int32(t.Pressure),
			Expiration: emupb.Touch_NEVER_EXPIRE})
	}
	return c.call(ctx, "sendTouch", ev, nil)
}

// KeyPhase is down, up or both.
type KeyPhase int

const (
	KeyPress KeyPhase = iota
	KeyDown
	KeyUp
)

// Key sends a key by its DOM name ("GoBack", "GoHome", "AppSwitch",
// "ArrowUp", "Enter", "MediaPlayPause", "AudioVolumeUp", "Power"…), which the
// emulator turns into Android's keycode.
func (c *Client) Key(ctx context.Context, key string, phase KeyPhase) error {
	t := map[KeyPhase]emupb.KeyboardEvent_KeyEventType{KeyPress: emupb.KeyboardEvent_keypress, KeyDown: emupb.KeyboardEvent_keydown,
		KeyUp: emupb.KeyboardEvent_keyup}[phase]
	return c.call(ctx, "sendKey", &emupb.KeyboardEvent{EventType: t, Key: key}, nil)
}

// Text types text (any characters: the emulator sends them as typed).
func (c *Client) Text(ctx context.Context, text string) error {
	return c.call(ctx, "sendKey", &emupb.KeyboardEvent{Text: text}, nil)
}

// Rotate turns the device to degrees about its screen (0 portrait, 90 a
// quarter turn left, 180, 270), as the emulator's rotate buttons do.
func (c *Client) Rotate(ctx context.Context, degrees float32) error {
	return c.call(ctx, "setPhysicalModel", &emupb.PhysicalModelValue{Target: emupb.PhysicalModelValue_ROTATION,
		Value: &emupb.ParameterValue{Data: []float32{0, 0, degrees}}}, nil)
}

// Hinge sets a foldable's hinge, in degrees (0 closed, 180 flat).
func (c *Client) Hinge(ctx context.Context, degrees float32) error {
	return c.call(ctx, "setPhysicalModel", &emupb.PhysicalModelValue{Target: emupb.PhysicalModelValue_HINGE_ANGLE0,
		Value: &emupb.ParameterValue{Data: []float32{degrees}}}, nil)
}

// SetClipboard puts text on the device's clipboard.
func (c *Client) SetClipboard(ctx context.Context, text string) error {
	return c.call(ctx, "setClipboard", &emupb.ClipData{Text: text}, nil)
}

// Clipboard is the device's clipboard's text.
func (c *Client) Clipboard(ctx context.Context) (string, error) {
	var d emupb.ClipData
	return d.GetText(), c.call(ctx, "getClipboard", &emptypb.Empty{}, &d)
}
