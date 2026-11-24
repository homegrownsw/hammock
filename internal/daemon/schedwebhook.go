package daemon

// Webhook triggers (schedtriggers.go): POST /hook/SECRET on the remote
// access listener (HTTPS, the networks you chose in Settings ▸ Devices)
// starts a run of the task whose webhook has that secret, its body (up to
// maxHookBody) the event's details. Secrets are the daemon's own, never in
// the state clients are sent: ScheduleWebhook gives one, to you. A wrong
// secret gets the same 404 as any unknown path; the secrets are compared in
// constant time; calls are limited per address and in all.

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const (
	maxHookBody   = 64 << 10
	hookPerIP     = 30  // calls a minute from one address
	hookPerMinute = 120 // and from all
)

// hookLimiter counts webhook calls in the current minute.
type hookLimiter struct {
	mu     sync.Mutex
	window time.Time
	total  int
	perIP  map[string]int
}

// allow takes one call from ip, if there's room this minute.
func (l *hookLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.window) >= time.Minute {
		l.window, l.total, l.perIP = now, 0, map[string]int{}
	}
	src := failSource(ip)
	if l.total >= hookPerMinute || l.perIP[src] >= hookPerIP {
		return false
	}
	l.total++
	l.perIP[src]++
	return true
}

// handleHook is POST /hook/SECRET.
func (d *Daemon) handleHook(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	var local net.IP
	if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if h, _, err := net.SplitHostPort(la.String()); err == nil {
			local = net.ParseIP(h)
		}
	}
	if !d.remoteSourceOK(net.ParseIP(ip), local) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !d.hookRate.allow(ip, time.Now()) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	secret := strings.TrimPrefix(r.URL.Path, "/hook/")
	d.mu.Lock()
	s, t := d.hookFor(secret)
	d.mu.Unlock()
	if s == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "POST to it", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHookBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, fmt.Sprintf("the body is over %d KB", maxHookBody>>10), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "couldn't read the body", http.StatusBadRequest)
		return
	}
	ev := &schedEvent{Trigger: t, Kind: "webhook", Summary: "Webhook", Source: "a webhook's caller", Body: string(body)}
	if ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil {
		ev.Fields = append(ev.Fields, [2]string{"content_type", ct})
	}
	// What a few well-known senders say the call is about.
	for _, h := range []string{"X-GitHub-Event", "X-Gitlab-Event", "X-Event-Key"} {
		if v := r.Header.Get(h); v != "" {
			ev.Fields = append(ev.Fields, [2]string{strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(h, "X-"), "-", "_")), oneLine(v, 80)})
			if h == "X-GitHub-Event" {
				ev.Summary = "Webhook: GitHub " + oneLine(v, 40)
			}
		}
	}
	d.mu.Lock()
	cur := d.sched.list[s.Id]
	var run *pb.ScheduleRun
	queued := false
	if cur == nil || !hasTrigger(cur, t) {
		err = errors.New("gone")
	} else {
		d.audit("webhook", "schedule-webhook", cur.Id, cur.Name+" (from "+ip+")")
		run, queued, err = d.fireEvent(cur, ev)
	}
	d.mu.Unlock()
	switch {
	case err != nil && cur == nil:
		http.NotFound(w, r)
	case err != nil:
		http.Error(w, "the task can't run now: "+err.Error(), http.StatusConflict)
	case queued:
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "queued: it runs when its cooldown is up\n")
	default:
		w.WriteHeader(http.StatusAccepted)
		status := "started"
		if run != nil && run.Status == pb.ScheduleRun_STATUS_SKIPPED {
			status = "skipped: its last run is still going"
		}
		_, _ = io.WriteString(w, status+"\n")
	}
}

func hasTrigger(s *pb.Schedule, id string) bool {
	for _, t := range s.Events {
		if t.Id == id {
			return true
		}
	}
	return false
}

// hookFor is the schedule, and its webhook trigger, with secret, compared
// in constant time against every secret (their digests, so the same
// length). Callers hold d.mu.
func (d *Daemon) hookFor(secret string) (*pb.Schedule, string) {
	if secret == "" || len(secret) > 256 || strings.Contains(secret, "/") {
		return nil, ""
	}
	want := sha256.Sum256([]byte(secret))
	found := ""
	for id, sec := range d.sched.hooks {
		have := sha256.Sum256([]byte(sec))
		if subtle.ConstantTimeCompare(want[:], have[:]) == 1 {
			found = id
		}
	}
	if found == "" {
		return nil, ""
	}
	for _, s := range d.sched.list {
		if hasTrigger(s, found) {
			return s, found
		}
	}
	return nil, ""
}

// scheduleWebhook is ScheduleWebhook: a webhook trigger's URLs, its secret
// made afresh first if asked. Callers hold d.mu.
func (d *Daemon) scheduleWebhook(c *conn, m *pb.ScheduleWebhook) (*pb.ServerMessage, error) {
	s := d.sched.list[m.ScheduleId]
	if s == nil {
		return nil, fmt.Errorf("no such schedule %q", m.ScheduleId)
	}
	var t *pb.EventTrigger
	for _, x := range s.Events {
		if x.GetWebhook() != nil && (m.TriggerId == "" || x.Id == m.TriggerId) {
			t = x
			break
		}
	}
	if t == nil {
		return nil, errors.New("it has no webhook trigger: add one first")
	}
	if m.Regenerate || d.sched.hooks[t.Id] == "" {
		d.sched.hooks[t.Id] = randomToken(24)
		d.saveHooks()
		if m.Regenerate {
			d.audit(c.auditActor(), "schedule-webhook-new", s.Id, s.Name)
		}
	}
	path := "/hook/" + d.sched.hooks[t.Id]
	reply := &pb.ScheduleWebhookReply{Path: path}
	addrs, _ := d.reachAt()
	for _, a := range addrs {
		reply.Urls = append(reply.Urls, "https://"+a+path)
	}
	switch {
	case !d.remote.on:
		reply.Problem = "Remote access is off: turn it on in Settings ▸ Devices for the webhook to be reached"
	case len(reply.Urls) == 0:
		reply.Problem = "Remote access isn't listening on any network now"
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_ScheduleWebhookReply{ScheduleWebhookReply: reply}}, nil
}
