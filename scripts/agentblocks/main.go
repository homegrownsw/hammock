// agentblocks turns a Claude Code session into what a Mac would send a phone
// watching it, for playing it in the iPhone's demo (HMK_DEBUG_REPLAY,
// docs/debug-hooks.md). Development only:
//
//	go run ./scripts/agentblocks -src SESSION.jsonl -from N [-max-gap D] > replay.txt
//
// Each output line is "MS<tab>AgentView JSON": the session's first page (as
// the daemon sends it, up to line -from) at 0, then each line's blocks after
// it, MS after the one before (the session's own pace, no gap longer than
// -max-gap). Sessions hold private work: keep the output out of the repo.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/homegrownsw/hammock/internal/agentview"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/encoding/protojson"
)

func main() {
	src := flag.String("src", "", "the Claude Code session")
	from := flag.Int("from", 0, "lines before this one (1-based) are the history shown at first")
	maxGap := flag.Duration("max-gap", 3*time.Second, "the longest pause between lines")
	page := flag.Int("page", 150, "blocks in the first page (the daemon's agentBlocks)")
	flag.Parse()
	f, err := os.Open(*src)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<20), 64<<20)
	c := agentview.NewClaude("")
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	emit := func(ms int64, v *pb.AgentView) {
		b, err := protojson.Marshal(v)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(out, "%d\t%s\n", ms, b)
	}
	var last time.Time
	n := 0
	for sc.Scan() {
		n++
		line := sc.Bytes()
		var ts struct{ Timestamp time.Time }
		_ = json.Unmarshal(line, &ts)
		blocks := c.Feed(line)
		if n < *from {
			if !ts.Timestamp.IsZero() {
				last = ts.Timestamp
			}
			continue
		}
		if n == *from {
			all := c.Blocks()
			start := max(0, len(all)-*page)
			emit(0, &pb.AgentView{Replace: true, Blocks: all[start:len(all)-len(blocks)], HasMore: start > 0})
		}
		if len(blocks) == 0 {
			continue
		}
		gap := time.Duration(0)
		if !ts.Timestamp.IsZero() && !last.IsZero() {
			gap = min(max(ts.Timestamp.Sub(last), 0), *maxGap)
		}
		if !ts.Timestamp.IsZero() {
			last = ts.Timestamp
		}
		emit(gap.Milliseconds(), &pb.AgentView{Blocks: blocks, Working: true})
	}
	if err := sc.Err(); err != nil {
		log.Fatal(err)
	}
}
