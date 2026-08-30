package outbound

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/miekg/dns"
)

func TestResolveHybridTargetPreference(t *testing.T) {
	ipv4 := netip.MustParseAddr("192.0.2.1")
	ipv6 := netip.MustParseAddr("2001:db8::1")
	tests := []struct {
		name        string
		prefer      C.DNSPrefer
		want        netip.Addr
		wantQueries []uint16
	}{
		{name: "dual prefers IPv6", prefer: C.DualStack, want: ipv6, wantQueries: []uint16{dns.TypeAAAA}},
		{name: "IPv4 only", prefer: C.IPv4Only, want: ipv4, wantQueries: []uint16{dns.TypeA}},
		{name: "IPv6 only", prefer: C.IPv6Only, want: ipv6, wantQueries: []uint16{dns.TypeAAAA}},
		{name: "IPv4 preferred", prefer: C.IPv4Prefer, want: ipv4, wantQueries: []uint16{dns.TypeA}},
		{name: "IPv6 preferred", prefer: C.IPv6Prefer, want: ipv6, wantQueries: []uint16{dns.TypeAAAA}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var queries []uint16
			query := func(_ context.Context, _ string, queryType uint16) (netip.Addr, time.Duration, error) {
				queries = append(queries, queryType)
				if queryType == dns.TypeAAAA {
					return ipv6, time.Minute, nil
				}
				return ipv4, time.Minute, nil
			}
			got, err := resolveHybridTarget(context.Background(), "example.com", test.prefer, newHybridTargetCache(), query)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("target = %s, want %s", got, test.want)
			}
			if !equalQueryTypes(queries, test.wantQueries) {
				t.Fatalf("queries = %v, want %v", queries, test.wantQueries)
			}
		})
	}
}

func TestResolveHybridTargetDualFallsBackToIPv4(t *testing.T) {
	ipv4 := netip.MustParseAddr("192.0.2.1")
	var queries []uint16
	query := func(_ context.Context, _ string, queryType uint16) (netip.Addr, time.Duration, error) {
		queries = append(queries, queryType)
		if queryType == dns.TypeAAAA {
			return netip.Addr{}, 0, errors.New("no AAAA address")
		}
		return ipv4, time.Minute, nil
	}
	got, err := resolveHybridTarget(context.Background(), "example.com", C.DualStack, newHybridTargetCache(), query)
	if err != nil {
		t.Fatal(err)
	}
	if got != ipv4 {
		t.Fatalf("target = %s, want %s", got, ipv4)
	}
	if want := []uint16{dns.TypeAAAA, dns.TypeA}; !equalQueryTypes(queries, want) {
		t.Fatalf("queries = %v, want %v", queries, want)
	}
}

func TestResolveHybridTargetCacheTTL(t *testing.T) {
	cache := newHybridTargetCache()
	now := time.Unix(1000, 0)
	cache.now = func() time.Time { return now }
	ipv6 := netip.MustParseAddr("2001:db8::1")
	queries := 0
	query := func(_ context.Context, _ string, _ uint16) (netip.Addr, time.Duration, error) {
		queries++
		return ipv6, time.Minute, nil
	}

	for _, host := range []string{"Example.COM.", "example.com"} {
		got, err := resolveHybridTarget(context.Background(), host, C.DualStack, cache, query)
		if err != nil || got != ipv6 {
			t.Fatalf("target = %s, err = %v", got, err)
		}
	}
	if queries != 1 {
		t.Fatalf("queries before expiry = %d, want 1", queries)
	}

	now = now.Add(time.Minute)
	if _, err := resolveHybridTarget(context.Background(), "example.com", C.DualStack, cache, query); err != nil {
		t.Fatal(err)
	}
	if queries != 2 {
		t.Fatalf("queries after expiry = %d, want 2", queries)
	}
}

func equalQueryTypes(left, right []uint16) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestIsQUICInitial(t *testing.T) {
	tests := []struct {
		name    string
		packet  []byte
		initial bool
	}{
		{name: "v1 initial", packet: quicLongHeader(0xc0, quicVersion1), initial: true},
		{name: "v1 zero rtt", packet: quicLongHeader(0xd0, quicVersion1)},
		{name: "v1 handshake", packet: quicLongHeader(0xe0, quicVersion1)},
		{name: "v1 retry", packet: quicLongHeader(0xf0, quicVersion1)},
		{name: "v2 retry", packet: quicLongHeader(0xc0, quicVersion2)},
		{name: "v2 initial", packet: quicLongHeader(0xd0, quicVersion2), initial: true},
		{name: "v2 zero rtt", packet: quicLongHeader(0xe0, quicVersion2)},
		{name: "v2 handshake", packet: quicLongHeader(0xf0, quicVersion2)},
		{name: "short header", packet: []byte{0x40, 1, 2, 3, 4}},
		{name: "version negotiation", packet: quicLongHeader(0xc0, 0)},
		{name: "unknown version", packet: quicLongHeader(0xc0, 0xfaceb00c)},
		{name: "truncated", packet: []byte{0xc0, 0, 0, 0}},
		{name: "fixed bit absent", packet: quicLongHeader(0x80, quicVersion1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isQUICInitial(test.packet); got != test.initial {
				t.Fatalf("isQUICInitial() = %v, want %v", got, test.initial)
			}
		})
	}
}

func TestHybridInitialMessageWireFormat(t *testing.T) {
	raw, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	flow := &hybridQUICFlow{
		target: netip.MustParseAddrPort("[2606:4700:4700::1111]:443"),
		raw:    raw,
	}
	copy(flow.id[:], []byte("0123456789abcdef"))
	payload := quicLongHeader(0xc0, quicVersion1)
	message := flow.initialMessage(payload)

	if got := string(message[:4]); got != hybridQUICMagic {
		t.Fatalf("magic = %q", got)
	}
	if message[4] != hybridQUICInitial || !bytes.Equal(message[5:21], flow.id[:]) {
		t.Fatal("operation or flow id was not encoded")
	}
	if got := binary.BigEndian.Uint16(message[21:23]); got != raw.LocalAddr().(*net.UDPAddr).AddrPort().Port() {
		t.Fatalf("raw port = %d", got)
	}
	if message[23] != 6 || netip.AddrFrom16([16]byte(message[24:40])) != flow.target.Addr() {
		t.Fatal("target address was not encoded")
	}
	if got := binary.BigEndian.Uint16(message[40:42]); got != 443 {
		t.Fatalf("target port = %d", got)
	}
	if !bytes.Equal(message[42:], payload) {
		t.Fatal("Initial payload changed")
	}
}

func quicLongHeader(first byte, version uint32) []byte {
	packet := make([]byte, 5)
	packet[0] = first
	binary.BigEndian.PutUint32(packet[1:], version)
	return packet
}
