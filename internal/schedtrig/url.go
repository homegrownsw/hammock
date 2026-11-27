package schedtrig

// URL checks (pb.UrlCheckTrigger): a URL fetched every few minutes, firing
// as it starts failing, its body changes, or it starts containing a text.
// Like a command check, it has flags of its own in hmk (--url …), as a URL
// can have any of : @ , in it.

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// URL checks' bounds: how often (minutes), how long one fetch may take
// (seconds; never longer than how often), and the longest URL and text.
const (
	URLEveryDefault   = 5
	URLEveryMax       = 7 * 24 * 60
	URLTimeoutDefault = 30
	URLTimeoutMax     = 5 * 60
	MaxURL            = 2000
	MaxURLText        = 200
)

var urlOns = map[string]pb.UrlCheckTrigger_On{
	"fail": pb.UrlCheckTrigger_ON_FAIL, "change": pb.UrlCheckTrigger_ON_CHANGE, "contains": pb.UrlCheckTrigger_ON_CONTAINS,
}

// URLOn reads fail, change or contains.
func URLOn(s string) (pb.UrlCheckTrigger_On, error) {
	if on, ok := urlOns[strings.ToLower(strings.TrimSpace(s))]; ok {
		return on, nil
	}
	return 0, fmt.Errorf("%q: fail, change or contains", s)
}

// URLOnName is the word URLOn reads for on.
func URLOnName(on pb.UrlCheckTrigger_On) string {
	for k, v := range urlOns {
		if v == on {
			return k
		}
	}
	return "fail"
}

// URLEvery is how often u is fetched, in minutes.
func URLEvery(u *pb.UrlCheckTrigger) int {
	if u.GetEveryMinutes() <= 0 {
		return URLEveryDefault
	}
	return int(u.GetEveryMinutes())
}

// URLTimeout is how long one fetch of u may take.
func URLTimeout(u *pb.UrlCheckTrigger) time.Duration {
	secs := int(u.GetTimeoutSeconds())
	if secs <= 0 {
		secs = URLTimeoutDefault
	}
	return time.Duration(min(secs, URLTimeoutMax, URLEvery(u)*60)) * time.Second
}

// CheckURL says what's wrong with a URL check's URL: it's http or https,
// with a host, and no user name or password in it (they'd be shown).
func CheckURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	switch {
	case raw == "":
		return errors.New("which URL should the check fetch?")
	case err != nil:
		return fmt.Errorf("%q isn't a URL: %v", raw, err)
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%q: a URL check fetches http:// or https:// URLs", raw)
	case u.Host == "":
		return fmt.Errorf("%q has no host", raw)
	case u.User != nil:
		return errors.New("a URL check's URL can't have a user name or password in it")
	case len(raw) > MaxURL:
		return fmt.Errorf("the URL is too long (at most %d characters)", MaxURL)
	}
	return nil
}

// URLFlags writes a URL check as hmk's options for it.
func URLFlags(u *pb.UrlCheckTrigger) string {
	s := "--url " + shellQuote(u.Url) + " --url-every " + strconv.Itoa(URLEvery(u)) + " --url-on " + URLOnName(u.On)
	if u.On == pb.UrlCheckTrigger_ON_CONTAINS {
		s += " --url-text " + shellQuote(u.Text)
	}
	if u.TimeoutSeconds > 0 {
		s += " --url-timeout " + strconv.Itoa(int(u.TimeoutSeconds))
	}
	return s
}

// DescribeURL says what fires u: "https://example.com/health starts
// failing, checked every 5 minutes".
func DescribeURL(u *pb.UrlCheckTrigger) string {
	what := " starts failing"
	switch u.On {
	case pb.UrlCheckTrigger_ON_CHANGE:
		what = " changes"
	case pb.UrlCheckTrigger_ON_CONTAINS:
		what = " starts containing “" + u.Text + "”"
	}
	return u.Url + what + ", checked " + EveryWords(URLEvery(u))
}
