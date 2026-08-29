package outbound

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
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
