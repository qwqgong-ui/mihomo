package config

import (
	"net"
	"net/netip"
	"testing"
)

func TestParseIPv6UnavailablePreservesConfiguredState(t *testing.T) {
	originalVerifyIP6 := verifyIP6
	verifyIP6 = func() bool { return false }
	t.Cleanup(func() { verifyIP6 = originalVerifyIP6 })

	rawCfg := DefaultRawConfig()
	general := &General{IPv6: true}
	rawCfg.IPv6 = true
	rawCfg.DNS.IPv6 = true
	rawCfg.DNS.FakeIPRange6 = "2001:2::/48"
	rawCfg.Tun.Inet6Address = []netip.Prefix{netip.MustParsePrefix("fdfe:dcba:9876::1/126")}

	parseIPV6(rawCfg, general)

	if !general.IPv6 {
		t.Fatal("configured IPv6 intent was lost")
	}
	if general.IPv6Active {
		t.Fatal("runtime IPv6 remained active while the system had no usable IPv6")
	}
	if !rawCfg.DNS.IPv6 || rawCfg.DNS.FakeIPRange6 != "2001:2::/48" {
		t.Fatalf("configured DNS IPv6 state was mutated: enabled=%v fake=%q", rawCfg.DNS.IPv6, rawCfg.DNS.FakeIPRange6)
	}
	wantTun := netip.MustParsePrefix("fdfe:dcba:9876::1/126")
	if len(rawCfg.Tun.Inet6Address) != 1 || rawCfg.Tun.Inet6Address[0] != wantTun {
		t.Fatalf("configured TUN IPv6 addresses were mutated: %v", rawCfg.Tun.Inet6Address)
	}
}

func TestParseIPv6AvailableActivatesConfiguredState(t *testing.T) {
	originalVerifyIP6 := verifyIP6
	verifyIP6 = func() bool { return true }
	t.Cleanup(func() { verifyIP6 = originalVerifyIP6 })

	rawCfg := DefaultRawConfig()
	general := &General{IPv6: true}
	rawCfg.IPv6 = true

	parseIPV6(rawCfg, general)

	if !general.IPv6 || !general.IPv6Active {
		t.Fatalf("configured and active IPv6 = %v/%v, want true/true", general.IPv6, general.IPv6Active)
	}
}

func TestParseIPv6NotConfiguredStaysInactive(t *testing.T) {
	originalVerifyIP6 := verifyIP6
	verifyIP6 = func() bool { return true }
	t.Cleanup(func() { verifyIP6 = originalVerifyIP6 })

	rawCfg := DefaultRawConfig()
	general := &General{IPv6: false}
	rawCfg.IPv6 = false

	parseIPV6(rawCfg, general)

	if general.IPv6Active {
		t.Fatal("runtime IPv6 became active despite ipv6 not being configured")
	}
}

func TestSystemIPv6AvailableUsesVerifyIP6(t *testing.T) {
	originalVerifyIP6 := verifyIP6
	t.Cleanup(func() { verifyIP6 = originalVerifyIP6 })

	verifyIP6 = func() bool { return true }
	if !SystemIPv6Available() {
		t.Fatal("SystemIPv6Available() = false, want true")
	}

	verifyIP6 = func() bool { return false }
	if SystemIPv6Available() {
		t.Fatal("SystemIPv6Available() = true, want false")
	}
}

func TestUsableSystemIPv6RejectsTunAndTransitionAddresses(t *testing.T) {
	if !isIPv6TunnelInterface("Meta") || !isIPv6TunnelInterface("Teredo Tunneling Pseudo-Interface") {
		t.Fatal("known tunnel interface was accepted")
	}
	for _, address := range []string{"fdfe:dcba:9876::1", "2001:0:c612:7::1", "2001:2::1", "2002:c000:0204::1", "fe80::1"} {
		if isUsableSystemIPv6(netip.MustParseAddr(address)) {
			t.Fatalf("special-purpose IPv6 address %s was accepted", address)
		}
	}
	if !isUsableSystemIPv6(netip.MustParseAddr("240e::1")) {
		t.Fatal("native global IPv6 address was rejected")
	}
	if isIPv6TunnelInterface((&net.Interface{Name: "Ethernet"}).Name) {
		t.Fatal("physical interface was rejected")
	}
}
