package daemon

/*
#include <dns_sd.h>
#include <net/if.h>
#include <stdlib.h>
#include <arpa/inet.h>

// hmk_register advertises name on _hammock._tcp at port, on one interface,
// through mDNSResponder (no callback: nothing to hear back; deallocating
// the ref takes the advertisement down).
static int hmk_register(DNSServiceRef *ref, const char *ifname, const char *name, int port) {
	uint32_t idx = if_nametoindex(ifname);
	if (idx == 0) return -1;
	return DNSServiceRegister(ref, 0, idx, name, "_hammock._tcp", NULL, NULL, htons((uint16_t)port), 0, NULL, NULL, NULL);
}

#include <poll.h>
#include <string.h>
#include <stdio.h>
#include <time.h>

typedef struct { char name[256]; char type[256]; char domain[256]; uint32_t ifidx; } hmk_found;
typedef struct { hmk_found items[32]; int n; } hmk_browse_ctx;
typedef struct { char host[1025]; int port; int done; } hmk_resolve_ctx;

static void hmk_browse_cb(DNSServiceRef ref, DNSServiceFlags flags, uint32_t ifidx, DNSServiceErrorType err,
                          const char *name, const char *type, const char *domain, void *ctx) {
	hmk_browse_ctx *c = ctx;
	if (err != kDNSServiceErr_NoError || !(flags & kDNSServiceFlagsAdd) || c->n >= 32) return;
	hmk_found *f = &c->items[c->n++];
	strlcpy(f->name, name, sizeof f->name);
	strlcpy(f->type, type, sizeof f->type);
	strlcpy(f->domain, domain, sizeof f->domain);
	f->ifidx = ifidx;
}

static void hmk_resolve_cb(DNSServiceRef ref, DNSServiceFlags flags, uint32_t ifidx, DNSServiceErrorType err,
                           const char *fullname, const char *host, uint16_t port, uint16_t txtlen, const unsigned char *txt, void *ctx) {
	hmk_resolve_ctx *c = ctx;
	if (err == kDNSServiceErr_NoError) {
		strlcpy(c->host, host, sizeof c->host);
		c->port = ntohs(port);
	}
	c->done = 1;
}

// hmk_pump processes ref's results until *done or ms pass.
static void hmk_pump(DNSServiceRef ref, int ms, int *done) {
	int fd = DNSServiceRefSockFD(ref);
	struct timespec start; clock_gettime(CLOCK_MONOTONIC, &start);
	for (;;) {
		struct timespec now; clock_gettime(CLOCK_MONOTONIC, &now);
		int left = ms - (int)((now.tv_sec - start.tv_sec) * 1000 + (now.tv_nsec - start.tv_nsec) / 1000000);
		if (left <= 0 || (done && *done)) return;
		struct pollfd p = { fd, POLLIN, 0 };
		if (poll(&p, 1, left) <= 0) return;
		if (DNSServiceProcessResult(ref) != kDNSServiceErr_NoError) return;
	}
}

// hmk_browse looks for _hammock._tcp on interface ifname for ms, then
// resolves each found to "name\thost\tport\n" lines in out.
static int hmk_browse(const char *ifname, int ms, int total_ms, char *out, int outlen) {
	struct timespec start; clock_gettime(CLOCK_MONOTONIC, &start);
	uint32_t idx = if_nametoindex(ifname);
	if (idx == 0) return -1;
	hmk_browse_ctx bc; memset(&bc, 0, sizeof bc);
	DNSServiceRef ref;
	if (DNSServiceBrowse(&ref, 0, idx, "_hammock._tcp", "local.", hmk_browse_cb, &bc) != kDNSServiceErr_NoError) return -1;
	hmk_pump(ref, ms, NULL);
	DNSServiceRefDeallocate(ref);
	out[0] = 0;
	for (int i = 0; i < bc.n; i++) {
		struct timespec now; clock_gettime(CLOCK_MONOTONIC, &now);
		int left = total_ms - (int)((now.tv_sec - start.tv_sec) * 1000 + (now.tv_nsec - start.tv_nsec) / 1000000);
		if (left <= 0) break; // (out of time: what's resolved so far)
		hmk_resolve_ctx rc; memset(&rc, 0, sizeof rc);
		DNSServiceRef r;
		if (DNSServiceResolve(&r, 0, bc.items[i].ifidx, bc.items[i].name, bc.items[i].type, bc.items[i].domain, hmk_resolve_cb, &rc) != kDNSServiceErr_NoError) continue;
		hmk_pump(r, left < 1000 ? left : 1000, &rc.done);
		DNSServiceRefDeallocate(r);
		if (!rc.port || !rc.host[0]) continue;
		size_t used = strlen(out);
		snprintf(out + used, outlen - used, "%s\t%s\t%d\n", bc.items[i].name, rc.host, rc.port);
	}
	return bc.n;
}
*/
import "C"

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// bonjourRegister advertises t through mDNSResponder, returning what takes
// it down.
func bonjourRegister(t bonjourTarget) (func(), error) {
	ifn, name := C.CString(t.iface), C.CString(t.name)
	defer C.free(unsafe.Pointer(ifn))
	defer C.free(unsafe.Pointer(name))
	var ref C.DNSServiceRef
	if rc := C.hmk_register(&ref, ifn, name, C.int(t.port)); rc != 0 {
		return nil, fmt.Errorf("DNSServiceRegister on %s: %d", t.iface, int(rc))
	}
	return func() { C.DNSServiceRefDeallocate(ref) }, nil
}

// bonjourBrowse asks mDNSResponder who offers _hammock._tcp on iface (for
// wait), and where each listens: its host's addresses (IPv4) and port.
// mDNSResponder, not a query of our own: it answers from its cache and for
// this Mac's own advertisements, and it's what Local Network privacy lets
// through.
func bonjourBrowse(ctx context.Context, n hostNet, wait time.Duration) []bonjourFound {
	ifn := C.CString(n.iface)
	defer C.free(unsafe.Pointer(ifn))
	total := 2 * wait
	if d, ok := ctx.Deadline(); ok {
		total = time.Until(d)
	}
	buf := make([]byte, 64<<10)
	if C.hmk_browse(ifn, C.int(wait.Milliseconds()), C.int(total.Milliseconds()), (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))) <= 0 {
		return nil
	}
	var out []bonjourFound
	for _, line := range strings.Split(C.GoString((*C.char)(unsafe.Pointer(&buf[0]))), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 || ctx.Err() != nil {
			continue
		}
		port, _ := strconv.Atoi(f[2])
		lctx, cancel := context.WithTimeout(ctx, time.Second)
		ips, _ := net.DefaultResolver.LookupIP(lctx, "ip4", strings.TrimSuffix(f[1], "."))
		cancel()
		e := bonjourFound{name: f[0]}
		for _, ip := range ips {
			e.addrs = append(e.addrs, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		}
		if len(e.addrs) > 0 {
			out = append(out, e)
		}
	}
	return out
}
