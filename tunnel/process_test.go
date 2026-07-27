package tunnel

import (
	"net"
	"net/netip"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

type rawAddrWrapper struct {
	net.Addr
}

func (w rawAddrWrapper) RawAddr() net.Addr {
	return w.Addr
}

func TestProcessLookupEndpoints(t *testing.T) {
	rawSrc := netip.MustParseAddrPort("192.0.2.10:12345")
	rawDst := netip.MustParseAddrPort("198.51.100.20:443")
	metadata := &C.Metadata{
		SrcIP:      netip.MustParseAddr("203.0.113.1"),
		SrcPort:    54321,
		RawSrcAddr: rawAddrWrapper{Addr: net.TCPAddrFromAddrPort(rawSrc)},
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

func TestAddrPortFromNetAddr(t *testing.T) {
	t.Run("unmaps IPv4-mapped IPv6", func(t *testing.T) {
		mapped := netip.MustParseAddrPort("[::ffff:192.0.2.10]:12345")
		got, ok := addrPortFromNetAddr(net.TCPAddrFromAddrPort(mapped))
		want := netip.MustParseAddrPort("192.0.2.10:12345")
		if !ok || got != want {
			t.Fatalf("addrPortFromNetAddr() = %s, %v; want %s, true", got, ok, want)
		}
	})

	t.Run("rejects zero port", func(t *testing.T) {
		if got, ok := addrPortFromNetAddr(&net.TCPAddr{IP: net.IPv4(192, 0, 2, 10)}); ok || got.IsValid() {
			t.Fatalf("addrPortFromNetAddr() = %s, %v; want invalid, false", got, ok)
		}
	})
}
