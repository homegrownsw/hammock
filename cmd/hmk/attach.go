package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const detachKey = 0x1d // Ctrl-]

// resetModes undoes terminal modes an attached program may have enabled.
const resetModes = "\x1b[<u\x1b[?2004l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1004l\x1b[?25h\x1b[0m"

// attach connects this terminal to a pane until Ctrl-] or the pane exits.
func attach(c *client.Client, paneID string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("attach needs a terminal")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	out := os.Stdout
	out.WriteString("\x1b[?1049h\x1b[H\x1b[2J")
	defer func() {
		out.WriteString(resetModes + "\x1b[?1049l")
		term.Restore(fd, old)
		fmt.Println("[detached]")
	}()

	sendSize := func() {
		if cols, rows, err := term.GetSize(fd); err == nil {
			_ = c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Resize{Resize: &pb.Resize{PaneId: paneID, Cols: uint32(cols), Rows: uint32(rows)}}})
		}
	}
	sendSize()
	if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}}); err != nil {
		return err
	}
	_ = c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Focus{Focus: &pb.Focus{PaneId: paneID}}})

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	stdin := make(chan []byte)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				close(stdin)
				return
			}
			stdin <- append([]byte(nil), buf[:n]...)
		}
	}()

	for {
		select {
		case ev, ok := <-c.Events:
			if !ok {
				return fmt.Errorf("connection to hmkd lost")
			}
			if s := ev.GetPaneSnapshot(); s != nil && s.PaneId == paneID {
				// The snapshot starts with a full reset; clear instead so we stay
				// in our alternate screen.
				data := bytes.TrimPrefix(s.Data, []byte("\x1bc"))
				out.WriteString("\x1b[H\x1b[2J")
				out.Write(data)
			}
			if o := ev.GetPaneOutput(); o != nil && o.PaneId == paneID {
				out.Write(o.Data)
			}
			if st := ev.GetState(); st != nil {
				for _, p := range st.Panes {
					if p.Id == paneID && p.Exited {
						return nil
					}
				}
				for _, id := range st.DeletedPaneIds {
					if id == paneID {
						return nil
					}
				}
			}
		case b, ok := <-stdin:
			if !ok {
				return nil
			}
			if i := bytes.IndexByte(b, detachKey); i >= 0 {
				if i > 0 {
					_ = c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: b[:i]}}})
				}
				return nil
			}
			_ = c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: b}}})
		case <-winch:
			sendSize()
		}
	}
}
