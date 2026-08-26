package directrace

import (
	"net/netip"
	"testing"
)

func TestPreferRequiresCurrentRRSetAndFamily(t *testing.T) {
	host := "Game.Example."
	adapter := "DIRECT-test"
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	Store(host, adapter, v4)
	if preferred, loaded := Prefer("game.example", adapter, []netip.Addr{netip.MustParseAddr("192.0.2.2"), v4}); !loaded || preferred != v4 {
		t.Fatalf("preferred = %s, %v; want %s, true", preferred, loaded, v4)
	}
	if _, loaded := Prefer(host, adapter, []netip.Addr{netip.MustParseAddr("192.0.2.2")}); loaded {
		t.Fatal("stale winner survived RRset validation")
	}
	if _, loaded := Prefer(host, adapter, []netip.Addr{v6}); loaded {
		t.Fatal("IPv4 winner leaked into IPv6 family")
	}
}
