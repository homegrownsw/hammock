package daemon

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"time"
)

// worthTrying is addrs less those on a local network this computer isn't
// on now (lanSubnets, ADR-0016): an address on a network's subnet is tried
// only with an address of our own in it, so nothing is sent to whoever has
// that address on some other network (a café's). Names and other addresses
// (Tailscale, VPNs, extra addresses) are always tried.
func worthTrying(addrs, lanSubnets []string) []string {
	if len(lanSubnets) == 0 {
		return addrs
	}
	var subs []*net.IPNet
	for _, s := range lanSubnets {
		if _, n, err := net.ParseCIDR(s); err == nil {
			subs = append(subs, n)
		}
	}
	mine := slices.DeleteFunc(interfaceAddrs(), func(a ifAddr) bool {
		return strings.HasPrefix(a.name, "pdp_ip") // (cellular: its carrier's 10.x isn't your network)
	})
	var out []string
	for _, a := range addrs {
		host, _, err := net.SplitHostPort(a)
		ip := net.ParseIP(host)
		if err != nil || ip == nil {
			out = append(out, a)
			continue
		}
		var in *net.IPNet
		for _, n := range subs {
			if n.Contains(ip) {
				in = n
				break
			}
		}
		if in == nil {
			out = append(out, a)
			continue
		}
		for _, m := range mine {
			if in.Contains(m.ip) { // (its very address too: another Hammock on this computer)
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// dialStagger is how long each address gets before the next is tried
// alongside it: the first listed (the one reached last time, Tailscale's)
// usually wins without the others being tried at all.
var dialStagger = 250 * time.Millisecond

// raceDial tries addrs at once (each a little after the one before),
// returning the first to connect and closing any that connect after it.
// Each try gets per (and the whole, ctx). The error is the first one's,
// when none connects (errs has them all, by address).
func raceDial[T interface{ Close() error }](ctx context.Context, addrs []string, per time.Duration, dial func(context.Context, string) (T, error)) (T, string, error, map[string]error) {
	var zero T
	if len(addrs) == 0 {
		return zero, "", errors.New("no address to try"), nil
	}
	type result struct {
		v    T
		addr string
		err  error
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan result, len(addrs))
	stagger := dialStagger // (read once: a dial may outlive this call)
	for i, a := range addrs {
		go func() {
			if i > 0 {
				select {
				case <-time.After(time.Duration(i) * stagger):
				case <-ctx.Done():
					ch <- result{addr: a, err: ctx.Err()}
					return
				}
			}
			dctx, dcancel := context.WithTimeout(ctx, per)
			defer dcancel()
			v, err := dial(dctx, a)
			ch <- result{v, a, err}
		}()
	}
	errs := map[string]error{}
	var first error
	for n := 0; n < len(addrs); n++ {
		r := <-ch
		if r.err != nil {
			errs[r.addr] = r.err
			if first == nil && !errors.Is(r.err, context.Canceled) {
				first = r.err
			}
			continue
		}
		cancel() // the others give up; any that connect meanwhile are closed
		go func(left int) {
			for ; left > 0; left-- {
				if x := <-ch; x.err == nil {
					x.v.Close()
				}
			}
		}(len(addrs) - n - 1)
		return r.v, r.addr, nil, errs
	}
	if first == nil {
		first = ctx.Err()
	}
	return zero, "", first, errs
}

// onSubnets: addr's host is an address in one of subnets.
func onSubnets(addr string, subnets []string) bool {
	host, _, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil {
		return false
	}
	for _, s := range subnets {
		if _, n, err := net.ParseCIDR(s); err == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// tailscaleAddr: addr's host is a Tailscale address.
func tailscaleAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && overTailscale(ip)
}
