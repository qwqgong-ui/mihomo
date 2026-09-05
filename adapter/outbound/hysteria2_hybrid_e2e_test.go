package outbound

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	M "github.com/metacubex/sing/common/metadata"
)

// The hybrid client is driven entirely through net.PacketConn, so a virtual
// fabric is enough to run the whole registration exchange: which packets leave
// over the tunnel, which leave raw, and what a reply on either path turns into.

type virtualPacket struct {
	data []byte
	addr net.Addr
}

type virtualPacketConn struct {
	mu      sync.Mutex
	written []virtualPacket
	reads   chan virtualPacket
	closed  chan struct{}
	once    sync.Once
}

func newVirtualPacketConn() *virtualPacketConn {
	return &virtualPacketConn{
		reads:  make(chan virtualPacket, 16),
		closed: make(chan struct{}),
	}
}

func (c *virtualPacketConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	c.written = append(c.written, virtualPacket{data: append([]byte(nil), payload...), addr: addr})
	c.mu.Unlock()
	return len(payload), nil
}

func (c *virtualPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case packet := <-c.reads:
		return copy(p, packet.data), packet.addr, nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *virtualPacketConn) deliver(data []byte, from net.Addr) {
	select {
	case c.reads <- virtualPacket{data: append([]byte(nil), data...), addr: from}:
	case <-c.closed:
	}
}

func (c *virtualPacketConn) sent() []virtualPacket {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]virtualPacket(nil), c.written...)
}

// awaitSent waits for at least count packets, so a test never races the
// goroutines the connection runs on its own.
func (c *virtualPacketConn) awaitSent(t *testing.T, count int) []virtualPacket {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if sent := c.sent(); len(sent) >= count {
			return sent
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d packets were sent, want %d", len(c.sent()), count)
		}
		time.Sleep(time.Millisecond)
	}
}

func (c *virtualPacketConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *virtualPacketConn) LocalAddr() net.Addr {
	return net.UDPAddrFromAddrPort(netip.MustParseAddrPort("[2001:db8::9]:50000"))
}
func (c *virtualPacketConn) SetDeadline(time.Time) error      { return nil }
func (c *virtualPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *virtualPacketConn) SetWriteDeadline(time.Time) error { return nil }

type hybridHarness struct {
	conn     *hybridQUICPacketConn
	hy2      *virtualPacketConn
	raw      *virtualPacketConn
	fallback *virtualPacketConn
	relay    netip.AddrPort
}

func newHybridHarness(t *testing.T) *hybridHarness {
	t.Helper()
	h := &hybridHarness{
		hy2:      newVirtualPacketConn(),
		raw:      newVirtualPacketConn(),
		fallback: newVirtualPacketConn(),
		relay:    netip.MustParseAddrPort("[2001:db8::1]:443"),
	}
	h.conn = newHybridQUICPacketConn(h.hy2, h.relay,
		func() (net.PacketConn, error) { return h.raw, nil },
		func() (net.PacketConn, error) { return h.fallback, nil },
	)
	t.Cleanup(func() { h.conn.Close() })
	return h
}

func (h *hybridHarness) readWithin(t *testing.T, timeout time.Duration) (int, net.Addr, []byte) {
	t.Helper()
	type result struct {
		n    int
		addr net.Addr
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		buffer := make([]byte, 2048)
		n, addr, err := h.conn.ReadFrom(buffer)
		done <- result{n: n, addr: addr, data: buffer[:max(n, 0)], err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("ReadFrom: %v", got.err)
		}
		return got.n, got.addr, got.data
	case <-time.After(timeout):
		t.Fatal("ReadFrom did not deliver anything")
		return 0, nil, nil
	}
}

func hybridInitialPacket() []byte {
	packet := make([]byte, 5)
	packet[0] = 0xc0
	binary.BigEndian.PutUint32(packet[1:], quicVersion1)
	return packet
}

func hybridHandshakePacket() []byte {
	packet := make([]byte, 5)
	packet[0] = 0xe0 // long header, v1 Handshake: relayed over the tunnel
	binary.BigEndian.PutUint32(packet[1:], quicVersion1)
	return packet
}

// hybrid1RTTPacket is the only shape the raw path carries.
func hybrid1RTTPacket() []byte {
	return []byte{0x40, 0xaa, 0xbb, 0xcc, 0xdd, 0x00}
}

// A fake-IP flow reaches WriteTo as a name, which is the case the whole design
// turns on: the client registers the name and never resolves it.
func TestHybridClientRegistersNameThenRelaysRaw(t *testing.T) {
	h := newHybridHarness(t)
	destination := M.ParseSocksaddrHostPort("example.com", 443)

	initial := hybridInitialPacket()
	n, err := h.conn.WriteTo(initial, destination)
	if err != nil || n != len(initial) {
		t.Fatalf("WriteTo(initial) = %d, %v", n, err)
	}

	control := h.hy2.awaitSent(t, 1)[0]
	if control.addr.String() != hybridQUICControlAddress {
		t.Fatalf("registration went to %v, want the control address", control.addr)
	}
	if string(control.data[:4]) != hybridQUICMagic || control.data[4] != hybridQUICInitial {
		t.Fatalf("registration header = %x", control.data[:5])
	}
	if control.data[21] != hybridTargetDomain {
		t.Fatalf("address type = %d, want a domain", control.data[21])
	}
	if got := string(control.data[23 : 23+int(control.data[22])]); got != "example.com" {
		t.Fatalf("registered name = %q", got)
	}
	if !bytes.Equal(control.data[len(control.data)-len(initial):], initial) {
		t.Fatal("the Initial was not carried with the registration")
	}
	if sent := h.raw.sent(); len(sent) != 0 {
		t.Fatalf("%d packets went raw before the Initial was registered", len(sent))
	}

	// The registration is acknowledged with the address the name resolved to.
	// Until that lands the flow has no address to label a raw reply with, so it
	// stays on the tunnel.
	var id [16]byte
	copy(id[:], control.data[5:21])
	target := netip.MustParseAddrPort("[2606:4700:4700::1111]:443")
	h.hy2.deliver(hybridAckWithTarget(id, target), hybridControlAddr{})

	// The server's early replies arrive over the tunnel, attributed to the
	// target it resolved. That is what the client learns the target's
	// connection ID from, and it must surface as an ordinary read.
	h.hy2.deliver([]byte("server hello"), net.UDPAddrFromAddrPort(target))
	_, addr, data := h.readWithin(t, 2*time.Second)
	if string(data) != "server hello" {
		t.Fatalf("tunnel reply = %q", data)
	}
	if addr.String() != target.String() {
		t.Fatalf("tunnel reply came from %v, want %v", addr, target)
	}

	// The rest of the handshake is relayed over the tunnel, not sent raw: a raw
	// path carrying a Handshake packet would show an observer a QUIC connection
	// that never sent a ClientHello on the tuple it is handshaking over.
	handshake := hybridHandshakePacket()
	if _, err = h.conn.WriteTo(handshake, destination); err != nil {
		t.Fatalf("WriteTo(handshake): %v", err)
	}
	relayed := h.hy2.awaitSent(t, 2)[1]
	if relayed.addr.String() != hybridQUICControlAddress {
		t.Fatalf("the handshake went to %v, want the control address", relayed.addr)
	}
	if string(relayed.data[:4]) != hybridQUICMagic || relayed.data[4] != hybridQUICRelay {
		t.Fatalf("relay header = %x", relayed.data[:5])
	}
	if !bytes.Equal(relayed.data[5:21], id[:]) {
		t.Fatal("the relayed packet named another flow")
	}
	if !bytes.Equal(relayed.data[21:], handshake) {
		t.Fatal("the relayed packet was rewritten")
	}
	if sent := h.raw.sent(); len(sent) != 0 {
		t.Fatalf("%d handshake packets took the raw path", len(sent))
	}

	// A 1-RTT packet starts the handover. Until the raw path answers it is sent
	// over both, so a flow the server cannot match on the raw path stays alive
	// on the tunnel instead of being black-holed.
	oneRTT := hybrid1RTTPacket()
	if _, err = h.conn.WriteTo(oneRTT, destination); err != nil {
		t.Fatalf("WriteTo(1-RTT): %v", err)
	}
	rawSent := h.raw.awaitSent(t, 1)[0]
	if rawSent.addr.String() != h.relay.String() {
		t.Fatalf("raw packet went to %v, want the relay %v", rawSent.addr, h.relay)
	}
	if !bytes.Equal(rawSent.data, oneRTT) {
		t.Fatal("the raw packet was rewritten")
	}
	duringHandover := h.hy2.awaitSent(t, 3)[2]
	if duringHandover.data[4] != hybridQUICRelay || !bytes.Equal(duringHandover.data[21:], oneRTT) {
		t.Fatal("the 1-RTT packet was not mirrored over the tunnel during the handover")
	}
	if len(h.fallback.sent()) != 0 {
		t.Fatal("a registered flow used the fallback path")
	}

	// Once the raw path answers, the tunnel copy stops.
	h.raw.deliver([]byte("raw reply"), net.UDPAddrFromAddrPort(h.relay))
	_, _, data = h.readWithin(t, 2*time.Second)
	if string(data) != "raw reply" {
		t.Fatalf("raw reply = %q", data)
	}
	tunnelBefore := len(h.hy2.sent())
	if _, err = h.conn.WriteTo(oneRTT, destination); err != nil {
		t.Fatal(err)
	}
	h.raw.awaitSent(t, 2)
	if len(h.hy2.sent()) != tunnelBefore {
		t.Fatal("the tunnel still carried a 1-RTT packet after the raw path answered")
	}
}

// A rejected registration must move the flow back to the tunnel. The server
// holds no flow for that id, so the raw path would never answer.
func TestHybridClientFallsBackWhenRegistrationRejected(t *testing.T) {
	h := newHybridHarness(t)
	destination := M.ParseSocksaddrHostPort("example.com", 443)

	if _, err := h.conn.WriteTo(hybridInitialPacket(), destination); err != nil {
		t.Fatal(err)
	}
	control := h.hy2.awaitSent(t, 1)[0]
	var id [16]byte
	copy(id[:], control.data[5:21])

	h.hy2.deliver(hybridAckMessage(id, 1), hybridControlAddr{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := h.conn.WriteTo(hybrid1RTTPacket(), destination); err != nil {
			t.Fatal(err)
		}
		if len(h.fallback.sent()) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a rejected flow never moved off the raw path")
		}
		time.Sleep(time.Millisecond)
	}
	if got := h.fallback.sent()[0].addr.String(); got != destination.String() {
		t.Fatalf("fallback packet went to %v, want %v", got, destination)
	}
}

// Only a flow that starts with an Initial carries a registration, so anything
// else must stay on the ordinary tunnel path.
func TestHybridClientKeepsUnregisteredFlowsOnTheTunnel(t *testing.T) {
	h := newHybridHarness(t)
	destination := M.ParseSocksaddrHostPort("example.com", 443)

	if _, err := h.conn.WriteTo(hybridHandshakePacket(), destination); err != nil {
		t.Fatal(err)
	}
	if sent := h.fallback.awaitSent(t, 1); sent[0].addr.String() != destination.String() {
		t.Fatalf("packet went to %v, want %v", sent[0].addr, destination)
	}
	if len(h.raw.sent()) != 0 {
		t.Fatal("an unregistered flow used the raw path")
	}
	if len(h.hy2.sent()) != 0 {
		t.Fatal("an unregistered flow sent a control message")
	}
}

// An ineligible destination never reaches the relay at all.
func TestHybridClientRejectsIneligibleDestinations(t *testing.T) {
	tests := []struct {
		name        string
		destination net.Addr
	}{
		{name: "non 443", destination: M.ParseSocksaddrHostPort("example.com", 8443)},
		{name: "private", destination: net.UDPAddrFromAddrPort(netip.MustParseAddrPort("192.168.1.1:443"))},
		{name: "loopback", destination: net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:443"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newHybridHarness(t)
			if _, err := h.conn.WriteTo(hybridInitialPacket(), test.destination); err != nil {
				t.Fatal(err)
			}
			h.fallback.awaitSent(t, 1)
			if len(h.hy2.sent()) != 0 {
				t.Fatal("an ineligible destination was registered")
			}
			if len(h.raw.sent()) != 0 {
				t.Fatal("an ineligible destination used the raw path")
			}
		})
	}
}

// A raw path the server never matched must not black-hole the connection. The
// flow keeps every 1-RTT packet on the tunnel as well, and once the handover
// window has passed with no answer it stops writing raw altogether.
func TestHybridClientKeepsTunnelWhenRawPathNeverAnswers(t *testing.T) {
	h := newHybridHarness(t)
	destination := M.ParseSocksaddrHostPort("example.com", 443)

	if _, err := h.conn.WriteTo(hybridInitialPacket(), destination); err != nil {
		t.Fatal(err)
	}
	control := h.hy2.awaitSent(t, 1)[0]
	var id [16]byte
	copy(id[:], control.data[5:21])
	h.hy2.deliver(hybridAckWithTarget(id, netip.MustParseAddrPort("[2606:4700:4700::1111]:443")), hybridControlAddr{})

	oneRTT := hybrid1RTTPacket()
	deadline := time.Now().Add(2 * time.Second)
	var rawStopped bool
	for i := 0; !rawStopped && time.Now().Before(deadline); i++ {
		rawBefore := len(h.raw.sent())
		tunnelBefore := len(h.hy2.sent())
		if _, err := h.conn.WriteTo(oneRTT, destination); err != nil {
			t.Fatal(err)
		}
		// Every packet reaches the target over the tunnel, whatever the raw
		// path is doing.
		if len(h.hy2.sent()) == tunnelBefore {
			t.Fatal("a 1-RTT packet was not relayed over the tunnel")
		}
		if len(h.raw.sent()) == rawBefore {
			rawStopped = true
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !rawStopped {
		t.Fatal("the flow kept writing to a raw path that never answered")
	}
	if len(h.fallback.sent()) != 0 {
		t.Fatal("a registered flow used the fallback path")
	}
}
