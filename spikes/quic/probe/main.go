// probe watches a pane's remote video the way the iPhone does (OpenMedia,
// then QUIC) and writes it as an Annex-B .h264 file, for checking the
// encoder end to end:
//
//	go run ./spikes/quic/probe -dir /tmp/hmk-dev -pane PANE -secs 5 -out out.h264
//	ffmpeg -i out.h264 -frames:v 1 frame.png
package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func main() {
	dir := flag.String("dir", "", "the daemon's state dir")
	paneID := flag.String("pane", "", "pane id")
	host := flag.String("host", "127.0.0.1", "the daemon's remote address (host only)")
	secs := flag.Int("secs", 5, "seconds to watch")
	out := flag.String("out", "out.h264", "Annex-B output")
	inBand := flag.Bool("inband", false, "frames in band instead of QUIC")
	maxPixels := flag.Int("max", 0, "max pixels (0: full size)")
	stall := flag.Duration("stall", 0, "over QUIC, after 2 s: lose every frame (no ack) for this long, then recover like a viewer")
	sim := flag.String("sim", "", "send SimulatorCall ops instead, op:args_json;…")
	input := flag.String("input", "", "send BrowserInput ops instead, op:args_json;op:args_json… (e.g. mouse:{\"type\":\"down\",\"x\":0.1,\"y\":0.1})")
	flag.Parse()
	ctx := context.Background()
	c, err := client.Dial(ctx, filepath.Join(*dir, "run", "hmkd.sock"), "probe", "probe")
	if err != nil {
		log.Fatal(err)
	}
	if *sim != "" {
		for _, op := range strings.Split(*sim, ";") {
			name, args, _ := strings.Cut(op, ":")
			r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: *paneID, Op: name, ArgsJson: args}}})
			if err != nil {
				log.Fatal(err)
			}
			if name == "copy" {
				fmt.Println(r.GetSimulatorResult().GetJson())
			}
			time.Sleep(300 * time.Millisecond)
		}
		return
	}
	if *input != "" {
		for _, op := range strings.Split(*input, ";") {
			name, args, _ := strings.Cut(op, ":")
			if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserInput{BrowserInput: &pb.BrowserInput{PaneId: *paneID, Op: name, ArgsJson: args}}}); err != nil {
				log.Fatal(err)
			}
			time.Sleep(30 * time.Millisecond)
		}
		// Anything sent back (copied text, a menu).
		for deadline := time.After(time.Second); ; {
			select {
			case ev := <-c.Events:
				if cb := ev.GetBrowserClipboard(); cb != nil {
					fmt.Printf("clipboard: %q\n", cb.Text)
				}
				if bm := ev.GetBrowserMenu(); bm != nil {
					fmt.Printf("menu %s: %d items\n", bm.Kind, len(bm.Items))
				}
				continue
			case <-deadline:
			}
			break
		}
		return
	}
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: *paneID, InBand: *inBand, MaxPixels: uint32(*maxPixels)}}})
	if err != nil {
		log.Fatal(err)
	}
	t := r.GetMediaTicket()
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	var mu sync.Mutex
	start := time.Now()
	frames, keys, bytes := 0, 0, 0
	var w, h int
	write := func(key bool, fw, fh int, data []byte) {
		mu.Lock()
		defer mu.Unlock()
		frames++
		bytes += len(data)
		if key {
			keys++
		}
		w, h = fw, fh
		for len(data) >= 4 {
			n := int(binary.BigEndian.Uint32(data))
			if 4+n > len(data) {
				break
			}
			f.Write([]byte{0, 0, 0, 1})
			f.Write(data[4 : 4+n])
			data = data[4+n:]
		}
	}
	if *inBand {
		deadline := time.After(time.Duration(*secs) * time.Second)
		for {
			select {
			case ev := <-c.Events:
				if mf := ev.GetMediaFrame(); mf != nil {
					write(mf.Keyframe, int(mf.Width), int(mf.Height), mf.Data)
					c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_MediaAck{MediaAck: &pb.MediaAck{SessionId: t.SessionId, Seqs: []uint32{mf.Seq}}}})
				}
				continue
			case <-deadline:
			}
			break
		}
	} else {
		qc, err := quic.DialAddr(ctx, net.JoinHostPort(*host, strconv.Itoa(int(t.QuicPort))),
			&tls.Config{InsecureSkipVerify: true, NextProtos: []string{"hmk-media/1"}}, &quic.Config{MaxIncomingUniStreams: 1 << 20})
		if err != nil {
			log.Fatal(err)
		}
		ctl, _ := qc.OpenStream()
		ctl.Write(t.Ticket)
		var cmu sync.Mutex
		// In order, like a viewer: a frame waits for its predecessor; a
		// keyframe or refresh skips any gap.
		var last uint32
		pending := map[uint32][]byte{}
		refreshes, skipped := 0, 0
		stallFrom := time.Now().Add(2 * time.Second)
		var order sync.Mutex
		show := func(b []byte) {
			write(b[4]&1 != 0, int(binary.BigEndian.Uint16(b[13:])), int(binary.BigEndian.Uint16(b[15:])), b[17:])
		}
		accept := func(b []byte) {
			order.Lock()
			defer order.Unlock()
			seq := binary.BigEndian.Uint32(b)
			if seq <= last {
				return
			}
			if b[4]&3 != 0 || last == 0 {
				if b[4]&2 != 0 {
					refreshes++
				}
				for s := range pending {
					if s < seq {
						delete(pending, s)
						skipped++
					}
				}
				skipped += int(seq - last - 1)
			} else if seq != last+1 {
				pending[seq] = b
				return
			}
			show(b)
			last = seq
			for p, ok := pending[last+1]; ok; p, ok = pending[last+1] {
				delete(pending, last+1)
				show(p)
				last++
			}
		}
		defer func() {
			fmt.Printf("refreshes %d, frames skipped %d, still waiting %d\n", refreshes, skipped, len(pending))
		}()
		go func() {
			for {
				st, err := qc.AcceptUniStream(ctx)
				if err != nil {
					return
				}
				go func() {
					b, err := io.ReadAll(st)
					if err != nil || len(b) < 17 {
						return
					}
					if now := time.Now(); *stall > 0 && now.After(stallFrom) && now.Before(stallFrom.Add(*stall)) {
						return // lost
					}
					seq := binary.BigEndian.Uint32(b)
					rec := []byte{1, 0, 0, 0, 0}
					binary.BigEndian.PutUint32(rec[1:], seq)
					cmu.Lock()
					ctl.Write(rec)
					cmu.Unlock()
					accept(b)
				}()
			}
		}()
		time.Sleep(time.Duration(*secs) * time.Second)
		qc.CloseWithError(0, "")
	}
	mu.Lock()
	defer mu.Unlock()
	el := time.Since(start).Seconds()
	fmt.Printf("%d frames (%d keyframes) %dx%d, %.1f fps, %.2f Mbit/s\n", frames, keys, w, h, float64(frames)/el, float64(bytes*8)/el/1e6)
	f.Close()
}
