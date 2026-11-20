package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// fakeDaemon answers Hello, then keeps reading (and so answering pings)
// if answer is set, or falls silent: a connection that died without a word.
func fakeDaemon(t *testing.T, answer bool) string {
	t.Helper()
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		_, b, err := ws.Read(r.Context())
		if err != nil {
			return
		}
		hello := &pb.ClientMessage{}
		_ = proto.Unmarshal(b, hello)
		reply, _ := proto.Marshal(&pb.ServerMessage{ReplyTo: hello.Id, Msg: &pb.ServerMessage_HelloReply{HelloReply: &pb.HelloReply{}}})
		if ws.Write(r.Context(), websocket.MessageBinary, reply) != nil {
			return
		}
		if answer {
			go func() {
				for {
					if _, _, err := ws.Read(context.Background()); err != nil {
						return
					}
				}
			}()
		}
		<-stop
	}))
	t.Cleanup(func() { close(stop); srv.Close() })
	return "ws" + srv.URL[len("http"):] + "/v1"
}

func dialFake(t *testing.T, url string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dialURL(ctx, url, http.DefaultClient, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// A daemon that stops answering is dropped within a few pings; one that
// answers stays.
func TestKeepAliveDropsSilentDaemon(t *testing.T) {
	alive := dialFake(t, fakeDaemon(t, true))
	silent := dialFake(t, fakeDaemon(t, false))
	alive.KeepAlive(50*time.Millisecond, time.Second)
	silent.KeepAlive(50*time.Millisecond, time.Second)
	select {
	case <-silent.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a daemon answering nothing wasn't dropped")
	}
	if _, err := silent.Call(context.Background(), &pb.ClientMessage{}); err == nil {
		t.Fatal("a call on a dropped connection went through")
	}
	select {
	case <-alive.Done():
		t.Fatal("a daemon answering its pings was dropped")
	case <-time.After(500 * time.Millisecond):
	}
}

// CheckAlive drops a silent daemon within its own wait, however long the
// pings' interval; one that answers stays.
func TestCheckAlive(t *testing.T) {
	alive := dialFake(t, fakeDaemon(t, true))
	silent := dialFake(t, fakeDaemon(t, false))
	alive.KeepAlive(time.Hour, 200*time.Millisecond)
	silent.KeepAlive(time.Hour, 200*time.Millisecond)
	alive.CheckAlive()
	silent.CheckAlive()
	select {
	case <-silent.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a daemon answering nothing wasn't dropped when checked")
	}
	select {
	case <-alive.Done():
		t.Fatal("a daemon answering its ping was dropped when checked")
	case <-time.After(500 * time.Millisecond):
	}
}
