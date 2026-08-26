package outbound

import (
	"context"
	"net/netip"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

func TestRemoteResolveUDPPreservesDomain(t *testing.T) {
	base := NewBase(BaseOption{RemoteDNS: true})
	metadata := &C.Metadata{
		NetWork: C.UDP,
		Host:    "quic.example",
		DstPort: 443,
		DNSMode: C.DNSFakeIP,
	}

	if err := base.ResolveUDP(context.Background(), metadata); err != nil {
		t.Fatalf("ResolveUDP() error = %v", err)
	}
	if metadata.Host != "quic.example" {
		t.Fatalf("ResolveUDP() host = %q", metadata.Host)
	}
	if metadata.DstIP.IsValid() {
		t.Fatalf("ResolveUDP() unexpectedly resolved domain to %s", metadata.DstIP)
	}
}

func TestRemoteResolveUDPUsesMappedIP(t *testing.T) {
	base := NewBase(BaseOption{RemoteDNS: true})
	for _, dnsMode := range []C.DNSMode{C.DNSMapping, C.DNSHosts} {
		t.Run(dnsMode.String(), func(t *testing.T) {
			mappedIP := netip.MustParseAddr("203.0.113.8")
			metadata := &C.Metadata{
				NetWork: C.UDP,
				Host:    "mapped.example",
				DstIP:   mappedIP,
				DstPort: 443,
				DNSMode: dnsMode,
			}

			if err := base.ResolveUDP(context.Background(), metadata); err != nil {
				t.Fatalf("ResolveUDP() error = %v", err)
			}
			if metadata.Host != "" {
				t.Fatalf("ResolveUDP() retained auxiliary host %q", metadata.Host)
			}
			if metadata.DstIP != mappedIP {
				t.Fatalf("ResolveUDP() destination = %s, want %s", metadata.DstIP, mappedIP)
			}
		})
	}
}

func TestRemoteResolveUDPNeverLocallyResolvesIncompleteMapping(t *testing.T) {
	base := NewBase(BaseOption{RemoteDNS: true})
	metadata := &C.Metadata{
		NetWork: C.UDP,
		Host:    "remote-only.example",
		DstPort: 443,
		DNSMode: C.DNSMapping,
	}

	if err := base.ResolveUDP(context.Background(), metadata); err != nil {
		t.Fatalf("ResolveUDP() error = %v", err)
	}
	if metadata.Host != "remote-only.example" || metadata.DstIP.IsValid() {
		t.Fatalf("ResolveUDP() unexpectedly changed remote-only target: %#v", metadata)
	}
}
