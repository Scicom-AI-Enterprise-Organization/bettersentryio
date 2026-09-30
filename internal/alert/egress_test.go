package alert

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestBlockedAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"10.43.0.1":       true, // a ClusterIP
		"172.16.5.4":      true,
		"192.168.1.1":     true,
		"127.0.0.1":       true,
		"::1":             true,
		"169.254.169.254": true, // cloud metadata
		"100.64.0.7":      true,
		"0.0.0.0":         true,
		"fd00::1":         true,
		"::ffff:10.0.0.1": true, // v4-mapped must not slip through
		"8.8.8.8":         false,
		"52.96.0.10":      false,
		"2606:4700::1111": false,
	} {
		if got := blockedAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("blockedAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

// The default alerter refuses a loopback webhook at dial time, and says why; the
// request never lands.
func TestAlerterRefusesPrivateWebhookByDefault(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()

	a := New(nil, quiet(), 1)
	err := a.TestChannel(context.Background(), "webhook", srv.URL)
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("TestChannel to %s: err = %v, want a private-address refusal", srv.URL, err)
	}
	if hits != 0 {
		t.Fatalf("hits = %d, want 0", hits)
	}

	a.AllowPrivateDestinations()
	if err := a.TestChannel(context.Background(), "webhook", srv.URL); err != nil {
		t.Fatalf("with private destinations allowed: %v", err)
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
}
