package outbound

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"

	M "github.com/metacubex/sing/common/metadata"
)

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

// The tunnel hands a fake-IP flow its original name, which is what lets the
// server resolve the destination instead of the client.
func TestParseHybridDestination(t *testing.T) {
	tests := []struct {
		name        string
		destination net.Addr
		want        hybridTarget
		wantOK      bool
	}{
		{
			name:        "udp address",
			destination: net.UDPAddrFromAddrPort(netip.MustParseAddrPort("[2606:4700:4700::1111]:443")),
			want:        hybridTarget{addr: netip.MustParseAddr("2606:4700:4700::1111"), port: 443},
			wantOK:      true,
		},
		{
			name:        "fqdn",
			destination: M.ParseSocksaddrHostPort("example.com", 443),
			want:        hybridTarget{name: "example.com", port: 443},
			wantOK:      true,
		},
		{
			name:        "fqdn keeps a non-443 port for the caller to reject",
			destination: M.ParseSocksaddrHostPort("example.com", 8443),
			want:        hybridTarget{name: "example.com", port: 8443},
			wantOK:      true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseHybridDestination(test.destination)
			if ok != test.wantOK || got != test.want {
				t.Fatalf("parseHybridDestination() = %+v, %v; want %+v, %v", got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestHybridTargetEligibility(t *testing.T) {
	tests := []struct {
		name   string
		target hybridTarget
		want   bool
	}{
		{name: "public v6", target: hybridTarget{addr: netip.MustParseAddr("2606:4700:4700::1111"), port: 443}, want: true},
		{name: "public v4", target: hybridTarget{addr: netip.MustParseAddr("1.1.1.1"), port: 443}, want: true},
		{name: "domain", target: hybridTarget{name: "example.com", port: 443}, want: true},
		{name: "wrong port", target: hybridTarget{addr: netip.MustParseAddr("1.1.1.1"), port: 8443}},
		{name: "private", target: hybridTarget{addr: netip.MustParseAddr("192.168.1.1"), port: 443}},
		{name: "loopback", target: hybridTarget{addr: netip.MustParseAddr("127.0.0.1"), port: 443}},
		// The default fake-IP pool sits in the benchmarking range, which passes
		// every ordinary "is this public" test.
		{name: "fake ip range is not private", target: hybridTarget{addr: netip.MustParseAddr("198.18.0.5"), port: 443}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.target.eligible(); got != test.want {
				t.Fatalf("eligible() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHybridInitialMessageWireFormat(t *testing.T) {
	payload := quicLongHeader(0xc0, quicVersion1)

	t.Run("ipv6", func(t *testing.T) {
		flow := &hybridQUICFlow{target: hybridTarget{addr: netip.MustParseAddr("2606:4700:4700::1111"), port: 443}}
		copy(flow.id[:], "0123456789abcdef")
		message := flow.initialMessage(payload)

		assertHybridHeader(t, message, flow.id)
		if message[21] != hybridTargetIPv6 {
			t.Fatalf("address type = %d, want %d", message[21], hybridTargetIPv6)
		}
		if netip.AddrFrom16([16]byte(message[22:38])) != flow.target.addr {
			t.Fatal("target address was not encoded")
		}
		if got := binary.BigEndian.Uint16(message[38:40]); got != 443 {
			t.Fatalf("target port = %d", got)
		}
		if !bytes.Equal(message[40:], payload) {
			t.Fatal("Initial payload changed")
		}
	})

	t.Run("ipv4", func(t *testing.T) {
		flow := &hybridQUICFlow{target: hybridTarget{addr: netip.MustParseAddr("1.1.1.1"), port: 443}}
		copy(flow.id[:], "0123456789abcdef")
		message := flow.initialMessage(payload)

		assertHybridHeader(t, message, flow.id)
		if message[21] != hybridTargetIPv4 {
			t.Fatalf("address type = %d, want %d", message[21], hybridTargetIPv4)
		}
		if netip.AddrFrom4([4]byte(message[22:26])) != flow.target.addr {
			t.Fatal("target address was not encoded")
		}
		if got := binary.BigEndian.Uint16(message[26:28]); got != 443 {
			t.Fatalf("target port = %d", got)
		}
		if !bytes.Equal(message[28:], payload) {
			t.Fatal("Initial payload changed")
		}
	})

	t.Run("domain", func(t *testing.T) {
		name := "example.com"
		flow := &hybridQUICFlow{target: hybridTarget{name: name, port: 443}}
		copy(flow.id[:], "0123456789abcdef")
		message := flow.initialMessage(payload)

		assertHybridHeader(t, message, flow.id)
		if message[21] != hybridTargetDomain {
			t.Fatalf("address type = %d, want %d", message[21], hybridTargetDomain)
		}
		if int(message[22]) != len(name) {
			t.Fatalf("name length = %d, want %d", message[22], len(name))
		}
		end := 23 + len(name)
		if string(message[23:end]) != name {
			t.Fatalf("name = %q, want %q", message[23:end], name)
		}
		if got := binary.BigEndian.Uint16(message[end : end+2]); got != 443 {
			t.Fatalf("target port = %d", got)
		}
		if !bytes.Equal(message[end+2:], payload) {
			t.Fatal("Initial payload changed")
		}
	})
}

// A rejected registration has to move the flow off the raw path: the server
// holds no flow for that id, so nothing there would ever answer.
func TestHybridAckRejectionFallsBack(t *testing.T) {
	conn := &hybridQUICPacketConn{
		flows:     make(map[hybridTarget]*hybridQUICFlow),
		flowsByID: make(map[[16]byte]*hybridQUICFlow),
	}
	flow := &hybridQUICFlow{target: hybridTarget{name: "example.com", port: 443}}
	copy(flow.id[:], "0123456789abcdef")
	conn.flows[flow.target] = flow
	conn.flowsByID[flow.id] = flow

	if !conn.handleAck(hybridAckMessage(flow.id, hybridAckOK)) {
		t.Fatal("an ack was not recognized")
	}
	if flow.rejected {
		t.Fatal("a successful ack rejected the flow")
	}

	if !conn.handleAck(hybridAckMessage(flow.id, 1)) {
		t.Fatal("a rejection was not recognized")
	}
	if !flow.rejected {
		t.Fatal("a rejection did not move the flow off the raw path")
	}

	if conn.handleAck([]byte("not an ack at all....")) {
		t.Fatal("unrelated data was treated as an ack")
	}
}

func hybridAckMessage(id [16]byte, status byte) []byte {
	message := make([]byte, 0, 22)
	message = append(message, hybridQUICMagic...)
	message = append(message, hybridQUICAck)
	message = append(message, id[:]...)
	return append(message, status)
}

// hybridAckWithTarget is the acknowledgement a successful registration gets: it
// carries the address the server resolved the name to, which is the only way a
// name flow can label what comes back on the raw path.
func hybridAckWithTarget(id [16]byte, target netip.AddrPort) []byte {
	message := hybridAckMessage(id, hybridAckOK)
	if addr := target.Addr(); addr.Is4() {
		v4 := addr.As4()
		message = append(message, hybridTargetIPv4)
		message = append(message, v4[:]...)
	} else {
		v6 := addr.As16()
		message = append(message, hybridTargetIPv6)
		message = append(message, v6[:]...)
	}
	return binary.BigEndian.AppendUint16(message, target.Port())
}

func assertHybridHeader(t *testing.T, message []byte, id [16]byte) {
	t.Helper()
	if got := string(message[:4]); got != hybridQUICMagic {
		t.Fatalf("magic = %q, want %q", got, hybridQUICMagic)
	}
	if message[4] != hybridQUICInitial {
		t.Fatalf("operation = %d, want %d", message[4], hybridQUICInitial)
	}
	if !bytes.Equal(message[5:21], id[:]) {
		t.Fatal("flow id was not encoded")
	}
}

func quicLongHeader(first byte, version uint32) []byte {
	packet := make([]byte, 5)
	packet[0] = first
	binary.BigEndian.PutUint32(packet[1:], version)
	return packet
}
