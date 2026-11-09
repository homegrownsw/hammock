//go:build !darwin

package daemon

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"time"
)

// bonjourRegister advertises t with Avahi, when it's installed
// (avahi-publish-service, which keeps it up while it runs). Avahi
// advertises on every interface it manages: a Linux box choosing one
// network for remote access is still advertised on the others, but only
// listens on the one chosen.
func bonjourRegister(t bonjourTarget) (func(), error) {
	path, err := exec.LookPath("avahi-publish-service")
	if err != nil {
		return nil, errors.New("no avahi-publish-service")
	}
	cmd := exec.Command(path, "--", t.name, "_hammock._tcp", strconv.Itoa(t.port)) // (a name starting with - isn't an option)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go cmd.Wait()
	return func() { _ = cmd.Process.Kill() }, nil
}

// bonjourBrowse asks n's network who offers _hammock._tcp (an mDNS question
// of our own: Avahi, if running, answers it as any responder does).
func bonjourBrowse(ctx context.Context, n hostNet, wait time.Duration) []bonjourFound {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	return mdnsQuery(ctx, n)
}
