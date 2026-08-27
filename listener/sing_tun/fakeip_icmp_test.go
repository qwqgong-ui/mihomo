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
		routeContext: back,
		winners:      make(map[uint16]netip.Addr),
		requests:     make(map[fakeIPICMPRequest][]byte),
		reporters:    make(map[fakeIPICMPRequest]netip.Addr),
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

func TestFakeIPICMPRaceRelaysErrorsWithoutLeakingRealIP(t *testing.T) {
	fakeIP := netip.MustParseAddr("198.18.0.10")
	clientIP := netip.MustParseAddr("192.0.2.100")
	firstIP := netip.MustParseAddr("192.0.2.1")
	secondIP := netip.MustParseAddr("192.0.2.2")
	routerIP := netip.MustParseAddr("192.0.2.254")
	otherRouterIP := netip.MustParseAddr("192.0.2.253")
	back := &recordingICMPContext{}
	first := &recordingICMPDestination{}
	second := &recordingICMPDestination{}
	race := &fakeIPICMPRace{
		fakeIP:     fakeIP,
		host:       "game.example",
		adapter:    "DIRECT-test",
		backWriter: ping.NewContextDestinationWriter(back, fakeIP),
		destinations: map[netip.Addr]tun.DirectRouteDestination{
			firstIP:  ping.NewDestinationWriter(first, firstIP),
			secondIP: ping.NewDestinationWriter(second, secondIP),
		},
		routeContext: back,
		winners:      make(map[uint16]netip.Addr),
		requests:     make(map[fakeIPICMPRequest][]byte),
		reporters:    make(map[fakeIPICMPRequest]netip.Addr),
	}

	request := makeICMPv4Packet(clientIP, fakeIP, 8, 77, 1, []byte("probe-payload"))
	if err := race.WritePacket(buf.As(request).ToOwned()); err != nil {
		t.Fatal(err)
	}

	// Routers only have to return the first bytes of the datagram they dropped.
	timeExceeded := makeICMPv4Error(routerIP, clientIP, 11, clientIP, firstIP, 77, 1, []byte("probe-pa"))
	if err := race.handleReply(firstIP, timeExceeded); err != nil {
		t.Fatal(err)
	}
	if back.count() != 1 {
		t.Fatalf("relayed error packets = %d, want 1", back.count())
	}
	relayed := back.last()
	if got := netip.AddrFrom4([4]byte(relayed[12:16])); got != routerIP {
		t.Fatalf("relayed source = %s, want the reporting router %s", got, routerIP)
	}
	if got := netip.AddrFrom4([4]byte(relayed[44:48])); got != fakeIP {
		t.Fatalf("embedded destination = %s, want Fake-IP %s", got, fakeIP)
	}
	if got := netip.AddrFrom4([4]byte(relayed[40:44])); got != clientIP {
		t.Fatalf("embedded source = %s, want %s", got, clientIP)
	}
	if binary.BigEndian.Uint16(relayed[52:54]) != 77 || binary.BigEndian.Uint16(relayed[54:56]) != 1 {
		t.Fatal("embedded identifier or sequence was rewritten")
	}
	assertICMPv4ChecksumTest(t, relayed[20:], "outer ICMP")
	assertIPv4ChecksumTest(t, relayed[28:48], "embedded IPv4 header")

	// The same hop reported by another candidate is one hop, not two.
	duplicate := makeICMPv4Error(otherRouterIP, clientIP, 11, clientIP, secondIP, 77, 1, []byte("probe-pa"))
	if err := race.handleReply(secondIP, duplicate); err != nil {
		t.Fatal(err)
	}
	if back.count() != 1 {
		t.Fatal("a second candidate's report of the same hop reached the application")
	}

	// An error for an echo this race never sent is not ours to relay.
	unknown := makeICMPv4Error(routerIP, clientIP, 11, clientIP, firstIP, 78, 9, nil)
	if err := race.handleReply(firstIP, unknown); err != nil {
		t.Fatal(err)
	}
	if back.count() != 1 {
		t.Fatal("unmatched error reached the application")
	}
}

func TestFakeIPICMPRaceRelaysIPv6Errors(t *testing.T) {
	fakeIP := netip.MustParseAddr("2001:db8:fa::1")
	clientIP := netip.MustParseAddr("2001:db8::100")
	candidateIP := netip.MustParseAddr("2001:db8::1")
	routerIP := netip.MustParseAddr("2001:db8::fe")
	back := &recordingICMPContext{}
	destination := &recordingICMPDestination{}
	race := &fakeIPICMPRace{
		fakeIP:     fakeIP,
		host:       "game.example",
		adapter:    "DIRECT-test",
		backWriter: ping.NewContextDestinationWriter(back, fakeIP),
		destinations: map[netip.Addr]tun.DirectRouteDestination{
			candidateIP: ping.NewDestinationWriter(destination, candidateIP),
		},
		routeContext: back,
		winners:      make(map[uint16]netip.Addr),
		requests:     make(map[fakeIPICMPRequest][]byte),
		reporters:    make(map[fakeIPICMPRequest]netip.Addr),
	}

	request := makeICMPv6Packet(clientIP, fakeIP, 128, 42, 7, []byte("probe-payload"))
	if err := race.WritePacket(buf.As(request).ToOwned()); err != nil {
		t.Fatal(err)
	}

	timeExceeded := makeICMPv6Error(routerIP, clientIP, 3, clientIP, candidateIP, 42, 7, []byte("probe-pa"))
	if err := race.handleReply(candidateIP, timeExceeded); err != nil {
		t.Fatal(err)
	}
	if back.count() != 1 {
		t.Fatalf("relayed error packets = %d, want 1", back.count())
	}
	relayed := back.last()
	if got := netip.AddrFrom16([16]byte(relayed[8:24])); got != routerIP {
		t.Fatalf("relayed source = %s, want the reporting router %s", got, routerIP)
	}
	if got := netip.AddrFrom16([16]byte(relayed[72:88])); got != fakeIP {
		t.Fatalf("embedded destination = %s, want Fake-IP %s", got, fakeIP)
	}
	assertICMPv6ChecksumTest(t, relayed, "outer ICMPv6")
}

func makeICMPv4Error(source, destination netip.Addr, typ byte, innerSource, innerDestination netip.Addr, identifier, sequence uint16, innerPayload []byte) []byte {
	inner := makeICMPv4Packet(innerSource, innerDestination, 8, identifier, sequence, innerPayload)
	packet := make([]byte, 20+8+len(inner))
	packet[0] = 0x45
	packet[8] = 64
	packet[9] = 1
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	copy(packet[12:16], source.AsSlice())
	copy(packet[16:20], destination.AsSlice())
	packet[20] = typ
	copy(packet[28:], inner)
	binary.BigEndian.PutUint16(packet[22:24], checksumICMPTest(packet[20:]))
	binary.BigEndian.PutUint16(packet[10:12], checksumICMPTest(packet[:20]))
	return packet
}

func makeICMPv6Packet(source, destination netip.Addr, typ byte, identifier, sequence uint16, payload []byte) []byte {
	packet := make([]byte, 40+8+len(payload))
	packet[0] = 0x60
	packet[6] = 58
	packet[7] = 64
	binary.BigEndian.PutUint16(packet[4:6], uint16(len(packet)-40))
	copy(packet[8:24], source.AsSlice())
	copy(packet[24:40], destination.AsSlice())
	packet[40] = typ
	binary.BigEndian.PutUint16(packet[44:46], identifier)
	binary.BigEndian.PutUint16(packet[46:48], sequence)
	copy(packet[48:], payload)
	binary.BigEndian.PutUint16(packet[42:44], checksumICMPv6Test(packet))
	return packet
}

func makeICMPv6Error(source, destination netip.Addr, typ byte, innerSource, innerDestination netip.Addr, identifier, sequence uint16, innerPayload []byte) []byte {
	inner := makeICMPv6Packet(innerSource, innerDestination, 128, identifier, sequence, innerPayload)
	packet := make([]byte, 40+8+len(inner))
	packet[0] = 0x60
	packet[6] = 58
	packet[7] = 64
	binary.BigEndian.PutUint16(packet[4:6], uint16(len(packet)-40))
	copy(packet[8:24], source.AsSlice())
	copy(packet[24:40], destination.AsSlice())
	packet[40] = typ
	copy(packet[48:], inner)
	binary.BigEndian.PutUint16(packet[42:44], checksumICMPv6Test(packet))
	return packet
}

func checksumICMPv6Test(packet []byte) uint16 {
	message := packet[40:]
	pseudoHeader := make([]byte, 40)
	copy(pseudoHeader[:16], packet[8:24])
	copy(pseudoHeader[16:32], packet[24:40])
	binary.BigEndian.PutUint32(pseudoHeader[32:36], uint32(len(message)))
	pseudoHeader[39] = 58
	stored := binary.BigEndian.Uint16(message[2:4])
	binary.BigEndian.PutUint16(message[2:4], 0)
	var sum uint32
	for _, block := range [][]byte{pseudoHeader, message} {
		for index := 0; index+1 < len(block); index += 2 {
			sum += uint32(binary.BigEndian.Uint16(block[index : index+2]))
		}
		if len(block)%2 == 1 {
			sum += uint32(block[len(block)-1]) << 8
		}
	}
	binary.BigEndian.PutUint16(message[2:4], stored)
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

func assertICMPv4ChecksumTest(t *testing.T, message []byte, name string) {
	t.Helper()
	stored := binary.BigEndian.Uint16(message[2:4])
	binary.BigEndian.PutUint16(message[2:4], 0)
	want := checksumICMPTest(message)
	binary.BigEndian.PutUint16(message[2:4], stored)
	if stored != want {
		t.Fatalf("%s checksum = %#04x, want %#04x", name, stored, want)
	}
}

func assertIPv4ChecksumTest(t *testing.T, ipHeader []byte, name string) {
	t.Helper()
	stored := binary.BigEndian.Uint16(ipHeader[10:12])
	binary.BigEndian.PutUint16(ipHeader[10:12], 0)
	want := checksumICMPTest(ipHeader)
	binary.BigEndian.PutUint16(ipHeader[10:12], stored)
	if stored != want {
		t.Fatalf("%s checksum = %#04x, want %#04x", name, stored, want)
	}
}

func assertICMPv6ChecksumTest(t *testing.T, packet []byte, name string) {
	t.Helper()
	stored := binary.BigEndian.Uint16(packet[42:44])
	want := checksumICMPv6Test(packet)
	if stored != want {
		t.Fatalf("%s checksum = %#04x, want %#04x", name, stored, want)
	}
}
