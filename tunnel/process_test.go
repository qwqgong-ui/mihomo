package tunnel

import (
	"net"
	"net/netip"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

func TestProcessLookupEndpoints(t *testing.T) {
	rawSrc := netip.MustParseAddrPort("192.0.2.10:12345")
	rawDst := netip.MustParseAddrPort("198.51.100.20:443")
	metadata := &C.Metadata{
		SrcIP:      netip.MustParseAddr("203.0.113.1"),
		SrcPort:    54321,
		RawSrcAddr: net.TCPAddrFromAddrPort(rawSrc),
		RawDstAddr: net.TCPAddrFromAddrPort(rawDst),
	}

	src, dst := processLookupEndpoints(metadata)
	if src != rawSrc || dst != rawDst {
		t.Fatalf("endpoints = %s -> %s, want %s -> %s", src, dst, rawSrc, rawDst)
	}

	metadata.RawSrcAddr = nil
	metadata.RawDstAddr = nil
	src, dst = processLookupEndpoints(metadata)
	wantSrc := metadata.SourceAddrPort()
	if src != wantSrc || dst.IsValid() {
		t.Fatalf("fallback endpoints = %s -> %s, want %s -> invalid", src, dst, wantSrc)
	}
}
