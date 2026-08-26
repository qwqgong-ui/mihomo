package sing_tun

import (
	"encoding/binary"
	"net/netip"
	"sync"
	"testing"

	tun "github.com/metacubex/sing-tun"
	"github.com/metacubex/sing-tun/ping"
	"github.com/metacubex/sing/common/buf"
)

type recordingICMPDestination struct {
	mu      sync.Mutex
	packets [][]byte
	closed  bool
}

func (d *recordingICMPDestination) WritePacket(packet *buf.Buffer) error {
	d.mu.Lock()
	d.packets = append(d.packets, append([]byte(nil), packet.Bytes()...))
	d.mu.Unlock()
	packet.Release()
	return nil
}
func (d *recordingICMPDestination) Close() error   { d.closed = true; return nil }
func (d *recordingICMPDestination) IsClosed() bool { return d.closed }
func (d *recordingICMPDestination) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.packets)
}
func (d *recordingICMPDestination) last() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]byte(nil), d.packets[len(d.packets)-1]...)
}

type recordingICMPContext struct {
	mu      sync.Mutex
	packets [][]byte
}

func (c *recordingICMPContext) WritePacket(packet []byte) error {
	c.mu.Lock()
	c.packets = append(c.packets, append([]byte(nil), packet...))
	c.mu.Unlock()
	return nil
}
func (c *recordingICMPContext) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.packets)
}
func (c *recordingICMPContext) last() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.packets[len(c.packets)-1]...)
}

func TestFakeIPICMPRacePinsWinnerAndNeverLeaksRealIP(t *testing.T) {
	fakeIP := netip.MustParseAddr("198.18.0.10")
	clientIP := netip.MustParseAddr("192.0.2.100")
	firstIP := netip.MustParseAddr("192.0.2.1")
	winnerIP := netip.MustParseAddr("192.0.2.2")
	back := &recordingICMPContext{}
	first := &recordingICMPDestination{}
	winner := &recordingICMPDestination{}
	race := &fakeIPICMPRace{
		fakeIP:     fakeIP,
		host:       "game.example",
		adapter:    "DIRECT-test",
		backWriter: ping.NewContextDestinationWriter(back, fakeIP),
		destinations: map[netip.Addr]tun.DirectRouteDestination{
			firstIP:  ping.NewDestinationWriter(first, firstIP),
			winnerIP: ping.NewDestinationWriter(winner, winnerIP),
		},
		winners:  make(map[uint16]netip.Addr),
		requests: make(map[fakeIPICMPRequest][]byte),
	}

	request := makeICMPv4Packet(clientIP, fakeIP, 8, 77, 1, []byte("probe"))
	if err := race.WritePacket(buf.As(request).ToOwned()); err != nil {
		t.Fatal(err)
	}
	if first.count() != 1 || winner.count() != 1 {
		t.Fatalf("initial fanout counts = %d, %d; want 1, 1", first.count(), winner.count())
	}
	if got := netip.AddrFrom4([4]byte(winner.last()[16:20])); got != winnerIP {
		t.Fatalf("outgoing destination = %s, want %s", got, winnerIP)
	}

	reply := makeICMPv4Packet(winnerIP, clientIP, 0, 77, 1, []byte("probe"))
	if err := race.handleReply(winnerIP, reply); err != nil {
		t.Fatal(err)
	}
	if back.count() != 1 {
		t.Fatalf("returned packets = %d, want 1", back.count())
	}
	returned := back.last()
	if got := netip.AddrFrom4([4]byte(returned[12:16])); got != fakeIP {
		t.Fatalf("application observed source %s, want Fake-IP %s", got, fakeIP)
	}

	lateLoser := makeICMPv4Packet(firstIP, clientIP, 0, 77, 1, []byte("probe"))
	if err := race.handleReply(firstIP, lateLoser); err != nil {
		t.Fatal(err)
	}
	if back.count() != 1 {
		t.Fatal("late loser reply reached application")
	}

	next := makeICMPv4Packet(clientIP, fakeIP, 8, 77, 2, []byte("next"))
	if err := race.WritePacket(buf.As(next).ToOwned()); err != nil {
		t.Fatal(err)
	}
	if first.count() != 1 || winner.count() != 2 {
		t.Fatalf("post-pin counts = %d, %d; want 1, 2", first.count(), winner.count())
	}
	wrongPayload := makeICMPv4Packet(winnerIP, clientIP, 0, 77, 2, []byte("forged"))
	_ = race.handleReply(winnerIP, wrongPayload)
	if back.count() != 1 {
		t.Fatal("payload-mismatched reply reached application")
	}
}

func TestParseFakeIPICMPPacketRejectsWrongTypeAndFamily(t *testing.T) {
	v4 := makeICMPv4Packet(netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("198.18.0.1"), 8, 1, 2, nil)
	if _, _, _, ok := parseFakeIPICMPPacket(v4, false, false); !ok {
		t.Fatal("valid IPv4 echo request rejected")
	}
	if _, _, _, ok := parseFakeIPICMPPacket(v4, true, false); ok {
		t.Fatal("echo request accepted as reply")
	}
	if _, _, _, ok := parseFakeIPICMPPacket(v4, false, true); ok {
		t.Fatal("IPv4 packet accepted in IPv6 race")
	}
}

func makeICMPv4Packet(source, destination netip.Addr, typ byte, identifier, sequence uint16, payload []byte) []byte {
	packet := make([]byte, 20+8+len(payload))
	packet[0] = 0x45
	packet[8] = 64
	packet[9] = 1
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	copy(packet[12:16], source.AsSlice())
	copy(packet[16:20], destination.AsSlice())
	packet[20] = typ
	binary.BigEndian.PutUint16(packet[24:26], identifier)
	binary.BigEndian.PutUint16(packet[26:28], sequence)
	copy(packet[28:], payload)
	binary.BigEndian.PutUint16(packet[22:24], checksumICMPTest(packet[20:]))
	binary.BigEndian.PutUint16(packet[10:12], checksumICMPTest(packet[:20]))
	return packet
}

func checksumICMPTest(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data[:2]))
		data = data[2:]
	}
	if len(data) == 1 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}
