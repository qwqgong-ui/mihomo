package outbound

import (
	"context"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

func TestVlessXUDPLeavesDomainForRemoteResolution(t *testing.T) {
	v := &Vless{
		Base:   NewBase(BaseOption{RemoteDNS: true}),
		option: &VlessOption{XUDP: true},
	}
	metadata := &C.Metadata{
		NetWork: C.UDP,
		Host:    "quic.example",
		DstPort: 443,
		DNSMode: C.DNSFakeIP,
	}

	if err := v.ResolveUDP(context.Background(), metadata); err != nil {
		t.Fatalf("ResolveUDP() error = %v", err)
	}
	if metadata.DstIP.IsValid() {
		t.Fatalf("ResolveUDP() unexpectedly resolved domain to %s", metadata.DstIP)
	}
}
