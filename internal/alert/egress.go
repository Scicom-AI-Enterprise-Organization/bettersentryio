package alert

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// Alert webhooks are URLs a user types, and the engine calls them from inside the
// cluster — with a failed test echoing up to 300 characters of the upstream's reply
// back to that user. Left open, that is a server-side request forgery primitive: probe
// the cluster network, reach the metadata service, read an internal endpoint's error
// page (VAPT: SSRF through the console's trusted path to the engine).
//
// The check runs in the dialer's Control hook, on the address actually being
// connected to — after DNS, and again for every redirect hop — so neither a hostname
// that resolves privately nor one that rebinds between check and use gets through.
// Slack, Teams and Telegram are public; an install that alerts through an internal
// relay opts out with --alert-allow-private.

var extraBlocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT, used by some overlays
}

// blockedAddr reports whether an alert may not be delivered to addr.
func blockedAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() {
		return true
	}
	for _, p := range extraBlocked {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func refusePrivate(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("alert destination %q: %w", address, err)
	}
	if blockedAddr(ap.Addr()) {
		return fmt.Errorf("alert destination %s is a private, loopback or link-local address; "+
			"webhooks must be public (--alert-allow-private for an internal relay)", ap.Addr())
	}
	return nil
}

// publicOnlyClient is the alerter's default client: the standard transport (proxy
// from the environment, HTTP/2, pooling) with a dialer that refuses non-public
// addresses. Behind an egress proxy the proxy is what gets dialled; an internal
// proxy therefore needs --alert-allow-private too.
func publicOnlyClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   refusePrivate,
	}).DialContext
	return &http.Client{Timeout: 10 * time.Second, Transport: t}
}

// AllowPrivateDestinations lets alerts reach private and loopback addresses, for an
// install whose webhook is an internal relay (and for tests, whose upstreams are
// httptest servers on 127.0.0.1).
func (a *Alerter) AllowPrivateDestinations() {
	a.http = &http.Client{Timeout: 10 * time.Second}
}
