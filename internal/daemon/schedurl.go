package daemon

// URL checks (schedtriggers.go): a trigger that fetches a URL every few
// minutes and fires when it starts failing (not 2xx, or unreachable), its
// body or status changes, or its body starts containing a text. They run
// beside command checks (schedcheck.go's watcher starts both): one fetch
// of a trigger at a time (one due meanwhile is skipped), for at most its
// time limit (then it counts as failing), reading at most maxURLBody of
// the body. Its first fetch only notes how things are. A change is any
// change of the body: a page with a clock or a token in it changes every
// time, so change suits an API or a plain page, and contains the rest.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
)

var (
	// maxURLBody is how much of a body is read (and hashed); maxURLKept how
	// much goes into a run.
	maxURLBody = int64(256 << 10)
	maxURLKept = 8 << 10
)

// urlState is what a URL check last saw (in its trigState).
type urlState struct {
	Started int64 `json:"started"` // when its last fetch began (ms)
	Seen    bool  `json:"seen"`    // it's had a first fetch
	Failed  bool  `json:"failed"`
	// Of its last fetch that passed (0: none yet): its HTTP status, a hash
	// of the body read, and whether the body had the trigger's text.
	Status   int    `json:"status"`
	Hash     string `json:"hash,omitempty"`
	Contains bool   `json:"contains,omitempty"`
}

// urlJob is one fetch, as its trigger was when it began.
type urlJob struct {
	schedule, trigger, key string
	url, text              string
	timeout                time.Duration
}

// urlKey is what a URL check watches: a change starts it afresh.
func urlKey(u *pb.UrlCheckTrigger) string {
	return "url:" + u.Url + "\x00" + strings.ToLower(u.Text)
}

// dueURLs are the URL checks to fetch now (each marked as running), while
// the daemon's state is in and the computer's fully awake. Takes d.mu.
func (d *Daemon) dueURLs(now time.Time) []urlJob {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || !d.sched.ready || !schedAwake() || d.checksHeld(now) {
		return nil
	}
	var jobs []urlJob
	for _, s := range d.sched.list {
		for _, t := range d.eventsOf(s) {
			u := t.GetUrl()
			if u == nil || d.sched.checking[t.Id] {
				continue
			}
			key := urlKey(u)
			st := d.trigStateFor(t.Id, key)
			if st.URL == nil {
				st.URL = &urlState{}
			}
			every := time.Duration(schedtrig.URLEvery(u)) * time.Minute
			if st.URL.Started > 0 && now.Sub(time.UnixMilli(st.URL.Started)) < every-time.Second {
				continue
			}
			st.URL.Started = now.UnixMilli()
			d.saveTrigState()
			d.sched.checking[t.Id] = true
			jobs = append(jobs, urlJob{schedule: s.Id, trigger: t.Id, key: key, url: u.Url, text: u.Text, timeout: schedtrig.URLTimeout(u)})
		}
	}
	return jobs
}

// urlClient fetches URL checks: no cookies kept, the environment's proxy.
var urlClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// urlResult is how a fetch went.
type urlResult struct {
	status int    // 0: no answer
	why    string // why it failed, in words ("" when it passed)
	body   []byte // its start, up to maxURLBody
	cut    bool   // there was more
}

// fetchURL fetches a URL check's URL (tests call it with a short time limit).
func fetchURL(ctx context.Context, j urlJob) urlResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return urlResult{why: "bad URL"}
	}
	req.Header.Set("User-Agent", "Hammock-URL-check")
	resp, err := urlClient.Do(req)
	if err != nil {
		var nerr net.Error
		switch {
		case ctx.Err() != nil || errors.As(err, &nerr) && nerr.Timeout():
			return urlResult{why: "timed out"}
		}
		return urlResult{why: "unreachable: " + shortNetErr(err)}
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxURLBody+1))
	r := urlResult{status: resp.StatusCode, body: body}
	if int64(len(body)) > maxURLBody {
		r.body, r.cut = body[:maxURLBody], true
	}
	switch {
	case ctx.Err() != nil:
		return urlResult{status: resp.StatusCode, why: "timed out"}
	case rerr != nil:
		r.why = "the answer broke off"
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		r.why = strings.TrimSpace(fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
	}
	return r
}

// shortNetErr is a network error without the URL Go puts first.
func shortNetErr(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && strings.HasPrefix(msg, "Get ") {
		msg = msg[i+2:]
	}
	return oneLine(msg, 120)
}

// runURL fetches one URL check, then fires its trigger if it should.
func (d *Daemon) runURL(j urlJob) {
	start, mono := schedNow(), time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), j.timeout)
	r := fetchURL(ctx, j)
	cancel()
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sched.checking, j.trigger)
	if d.checkSlept(start, mono) {
		if st := d.sched.trig[j.trigger]; st != nil && st.Key == j.key && st.URL != nil {
			st.URL.Started = 0 // (again once the wake's grace is over)
		}
		return
	}
	d.urlDone(j, r)
}

// urlDone notes how a fetch went, and fires its trigger if it should.
// Callers hold d.mu.
func (d *Daemon) urlDone(j urlJob, r urlResult) {
	s := d.sched.list[j.schedule]
	t := triggerOf(s, j.trigger)
	st := d.sched.trig[j.trigger]
	if s == nil || t == nil || t.GetUrl() == nil || st == nil || st.Key != j.key || st.URL == nil {
		return // changed or gone meanwhile
	}
	u := t.GetUrl()
	failed := r.why != ""
	result := fmt.Sprintf("%d %s", r.status, http.StatusText(r.status))
	switch {
	case r.why == "timed out":
		result = "Timed out"
	case failed:
		result = "Failed (" + r.why + ")"
	}
	prev := *st.URL
	st.URL.Seen, st.URL.Failed = true, failed
	// A failed fetch says nothing of the page: what it last had is kept, so
	// a blip neither counts as a change nor arms contains to fire again.
	hash, contains := prev.Hash, prev.Contains
	if !failed {
		sum := sha256.Sum256(r.body)
		hash = hex.EncodeToString(sum[:])
		contains = u.Text != "" && strings.Contains(strings.ToLower(string(r.body)), strings.ToLower(u.Text))
		st.URL.Status, st.URL.Hash, st.URL.Contains = r.status, hash, contains
	}
	d.saveTrigState()
	if due := d.noteChecked(t, schedNow()); due || u.LastResult != result {
		u.LastResult = result
		d.saveSchedule(s)
	}
	if !prev.Seen {
		return // the first look: how things are
	}
	var what string
	switch u.On {
	case pb.UrlCheckTrigger_ON_FAIL:
		if failed && !prev.Failed {
			what = "started failing (" + r.why + ")"
		}
	case pb.UrlCheckTrigger_ON_CHANGE:
		if !failed && prev.Status != 0 && (hash != prev.Hash || r.status != prev.Status) {
			what = "changed: " + result
		}
	case pb.UrlCheckTrigger_ON_CONTAINS:
		if !failed && prev.Status != 0 && contains && !prev.Contains {
			what = "now contains “" + u.Text + "”"
		}
	}
	if what == "" || d.eventsOf(s) == nil {
		return
	}
	fields := [][2]string{{"url", u.Url}, {"result", result}, {"status", fmt.Sprint(r.status)}, {"previous_status", fmt.Sprint(prev.Status)}}
	_, _, _ = d.fireEvent(s, &schedEvent{
		Trigger: t.Id, Kind: "url", Summary: "A URL check " + what, Source: "a web page",
		Fields: fields, Body: urlKept(r.body, r.cut),
	})
}

// urlKept is the start of a body for a run, cut to maxURLKept (saying so),
// as text ("" for a body that isn't).
func urlKept(body []byte, cut bool) string {
	if len(body) > maxURLKept {
		body, cut = body[:maxURLKept], true
		for i := 0; i < 3 && len(body) > 0 && !utf8.Valid(body); i++ { // (a character cut in two)
			body = body[:len(body)-1]
		}
	}
	if !utf8.Valid(body) {
		return "[not text]"
	}
	out := string(body)
	if cut {
		out += "\n[… cut]"
	}
	return out
}
