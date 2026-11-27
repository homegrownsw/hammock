package slack

// Socket Mode: Slack sends events down a WebSocket the app opens (with its
// app-level token), so nothing has to be reachable from the internet.
// Each envelope is acknowledged at once (its envelope_id sent back), before
// it's handled, or Slack sends it again. Slack asks for a new connection
// now and then (a disconnect envelope: refresh_requested, or a warning
// first); a connection that drops, or stops answering pings (the computer
// slept), is made again, after a wait that grows while it keeps failing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coder/websocket"
)

// Event is an Events API event that came down the socket.
type Event struct {
	ID    string          // its event_id (the same when Slack sends it again)
	Type  string          // the event's type: "message", "reaction_added"
	Event json.RawMessage // the event itself
	Retry int             // how many times Slack has tried to send it before
}

// Socket keeps a Socket Mode connection open until its context ends.
type Socket struct {
	App *Client // with the app-level token (xapp-)
	// OnEvent is each event, after it's acknowledged (on the socket's own
	// goroutine: hand slow work on).
	OnEvent func(Event)
	// OnState says the connection opened (err nil), or why it's down.
	OnState func(open bool, err error)
	// Backoff is how long to wait before the nth try in a row (n from 1);
	// nil: 1 s doubling to 2 minutes.
	Backoff func(n int) time.Duration
	// PingEvery is how often an open connection is pinged (0: 30 s); one
	// unanswered in 10 s (or PingEvery, if shorter) ends it.
	PingEvery time.Duration
}

// envelope is what Socket Mode sends.
type envelope struct {
	Type       string `json:"type"` // hello, events_api, disconnect, …
	EnvelopeID string `json:"envelope_id"`
	Reason     string `json:"reason"` // disconnect's
	Retry      int    `json:"retry_attempt"`
	Payload    struct {
		EventID string          `json:"event_id"`
		Event   json.RawMessage `json:"event"`
	} `json:"payload"`
}

// ErrDisabled: Socket Mode is off for the app (turned off in its settings).
var ErrDisabled = errors.New("Socket Mode is turned off for your Slack app")

// Run connects, and connects again whenever the connection ends, until
// ctx ends (nil), or Slack refuses the app-level token (that error).
func (s *Socket) Run(ctx context.Context) error {
	fails := 0
	for ctx.Err() == nil {
		started := time.Now()
		err := s.once(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if IsAuth(err) {
			s.state(false, err)
			return err
		}
		if time.Since(started) > time.Minute {
			fails = 0 // it was up a while: try again at once
		}
		fails++
		if err != nil {
			s.state(false, err)
		}
		wait := s.backoff(fails)
		if errors.Is(err, errRefresh) && fails == 1 {
			wait = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
	return nil
}

// errRefresh: Slack asked for a new connection.
var errRefresh = errors.New("Slack asked for a new connection")

func (s *Socket) backoff(n int) time.Duration {
	if s.Backoff != nil {
		return s.Backoff(n)
	}
	d := time.Second << min(n-1, 7)
	return min(d, 2*time.Minute)
}

func (s *Socket) state(open bool, err error) {
	if s.OnState != nil {
		s.OnState(open, err)
	}
}

// once makes one connection and reads it until it ends.
func (s *Socket) once(ctx context.Context) error {
	openCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	addr, err := s.App.OpenConnection(openCtx)
	if err != nil {
		cancel()
		return err
	}
	conn, _, err := websocket.Dial(openCtx, addr, nil)
	cancel()
	if err != nil {
		return fmt.Errorf("can't reach Slack: %w", err)
	}
	conn.SetReadLimit(4 << 20)
	defer conn.CloseNow()
	connCtx, stop := context.WithCancel(ctx)
	defer stop()
	go s.ping(connCtx, conn, stop)
	for {
		_, data, err := conn.Read(connCtx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("lost the connection to Slack: %w", err)
		}
		var env envelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		if env.EnvelopeID != "" {
			ack, _ := json.Marshal(map[string]string{"envelope_id": env.EnvelopeID})
			wctx, wcancel := context.WithTimeout(connCtx, 10*time.Second)
			err := conn.Write(wctx, websocket.MessageText, ack)
			wcancel()
			if err != nil {
				return fmt.Errorf("lost the connection to Slack: %w", err)
			}
		}
		switch env.Type {
		case "hello":
			s.state(true, nil)
		case "disconnect":
			_ = conn.Close(websocket.StatusNormalClosure, "")
			if env.Reason == "link_disabled" {
				return ErrDisabled
			}
			return errRefresh
		case "events_api":
			var head struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(env.Payload.Event, &head) != nil || head.Type == "" || s.OnEvent == nil {
				continue
			}
			s.OnEvent(Event{ID: env.Payload.EventID, Type: head.Type, Event: env.Payload.Event, Retry: env.Retry})
		}
	}
}

// ping pings conn every PingEvery, ending it (stop) when one goes
// unanswered: a connection a sleep broke reads nothing, and says nothing.
func (s *Socket) ping(ctx context.Context, conn *websocket.Conn, stop func()) {
	every := s.PingEvery
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pctx, cancel := context.WithTimeout(ctx, min(10*time.Second, every))
		err := conn.Ping(pctx)
		cancel()
		if err != nil && ctx.Err() == nil {
			stop()
			return
		}
	}
}
