package outbound

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/log"
	"github.com/metacubex/sing-quic/hysteria2"
)

const (
	hybridQUICControlAddress = "hybrid-quic.invalid:443"
	// HQV3 takes every long-header packet off the raw path: the handshake now
	// travels over the tunnel in both directions and the raw path carries
	// nothing but 1-RTT packets, which needs an op that relays a packet of an
	// already-registered flow. The magic is bumped so an HQV2 peer fails
	// cleanly instead of meeting an op it has no case for.
	hybridQUICMagic   = "HQV3"
	hybridQUICInitial = byte(1)
	hybridQUICAck     = byte(2)
	hybridQUICRelay   = byte(3)

	// hybridHandoverTimeout and hybridHandoverPackets bound the window in which
	// a flow sends its 1-RTT packets over both paths. Both have to be exceeded
	// before the raw path is written off: a burst can put eight packets on the
	// wire in less than the round trip an answer needs, and an application that
	// sends one packet and waits has not given the path a fair chance however
	// long it waits.
	hybridHandoverTimeout = 1500 * time.Millisecond
	hybridHandoverPackets = 8

	hybridTargetDomain = byte(0)
	hybridTargetIPv4   = byte(4)
	hybridTargetIPv6   = byte(6)

	hybridAckOK = byte(0)

	quicVersion1 = uint32(1)
	quicVersion2 = uint32(0x6b3343cf)
)

// hybridTarget is a destination as the application asked for it. A name is kept
// as a name: the server resolves it, so a fake-IP client never has to turn its
// own synthetic address back into a real one, and the destination stays
// independent of a ClientHello that ECH may have encrypted.
type hybridTarget struct {
	name string // set for a domain destination, empty otherwise
	addr netip.Addr
	port uint16
}

func (t hybridTarget) isDomain() bool { return t.name != "" }

func (t hybridTarget) String() string {
	host := t.name
	if !t.isDomain() {
		host = t.addr.String()
	}
	return net.JoinHostPort(host, strconv.FormatUint(uint64(t.port), 10))
}

// eligible reports whether this destination may use the raw relay at all. Port
// 443 only: raw QUIC to 443 is indistinguishable from ordinary traffic, and raw
// QUIC to anything else is not.
func (t hybridTarget) eligible() bool {
	if t.port != 443 {
		return false
	}
	if t.isDomain() {
		return true
	}
	return isHybridPublicTarget(t.addr)
}

func (t hybridTarget) netAddr() net.Addr {
	if t.isDomain() {
		return nil
	}
	return net.UDPAddrFromAddrPort(netip.AddrPortFrom(t.addr, t.port))
}

// parseHybridDestination reads the destination the tunnel handed to WriteTo. A
// fake-IP flow arrives as a name (tunnel clears DstIP for fake-IP and keeps
// Host), and everything else as an address.
func parseHybridDestination(destination net.Addr) (hybridTarget, bool) {
	if udpAddr, ok := destination.(*net.UDPAddr); ok && udpAddr != nil {
		addrPort := udpAddr.AddrPort()
		if !addrPort.IsValid() {
			return hybridTarget{}, false
		}
		return hybridTarget{addr: addrPort.Addr().Unmap(), port: addrPort.Port()}, true
	}
	host, portText, err := net.SplitHostPort(destination.String())
	if err != nil {
		return hybridTarget{}, false
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return hybridTarget{}, false
	}
	if addr, addrErr := netip.ParseAddr(host); addrErr == nil {
		return hybridTarget{addr: addr.Unmap(), port: uint16(port)}, true
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 255 {
		return hybridTarget{}, false
	}
	return hybridTarget{name: host, port: uint16(port)}, true
}

type hybridQUICPacketConn struct {
	hy2         net.PacketConn
	relay       netip.AddrPort
	newRaw      func() (net.PacketConn, error)
	newFallback func() (net.PacketConn, error)

	mu         sync.Mutex
	flows      map[hybridTarget]*hybridQUICFlow
	flowsByID  map[[16]byte]*hybridQUICFlow
	readCh     chan hybridQUICRead
	closed     chan struct{}
	closeOne   sync.Once
	fallback   net.PacketConn
	deadlineMu sync.Mutex
	readTimer  *time.Timer
}

type hybridQUICFlow struct {
	owner  *hybridQUICPacketConn
	id     [16]byte
	target hybridTarget
	raw    net.PacketConn
	// registered is set once the flow's Initial has gone out over the tunnel.
	// rejected is set if the server declined the registration, which is the one
	// case where continuing on the raw path would black-hole the connection.
	registered bool
	rejected   bool
	// phase tracks the handover from the tunnel to the raw path. Guarded by
	// owner.mu.
	phase          hybridPhase
	handoverExpiry time.Time
	handoverSent   int
	// resolved is the address the server reported for a registered name, and is
	// what the raw path's replies are labelled with. Guarded by owner.mu.
	resolved netip.AddrPort
}

// reportAddr is the address a datagram off the raw path is attributed to. For a
// literal destination that is the destination itself; for a name it is what the
// server resolved, which is the same address its replies over the tunnel
// carried, so the sender's mapping resolves a datagram from either path.
// hybridPhase is where a flow is in the move from the tunnel to the raw path.
// A flow starts on the tunnel, sends its first 1-RTT packets over both paths
// until one comes back raw, and only then leaves the tunnel behind. Sending on
// both is what keeps a flow the server could not match on the raw path alive
// rather than black-holed, and the overlap costs only duplicate packet numbers,
// which QUIC discards.
type hybridPhase int

const (
	hybridPhaseTunnel hybridPhase = iota
	hybridPhaseHandover
	hybridPhaseRaw
	// hybridPhaseStuck is a handover that timed out. The raw socket stays open
	// but unused; the flow lives out its life on the tunnel.
	hybridPhaseStuck
)

func (f *hybridQUICFlow) reportAddr() netip.AddrPort {
	if !f.target.isDomain() {
		return netip.AddrPortFrom(f.target.addr, f.target.port)
	}
	f.owner.mu.Lock()
	defer f.owner.mu.Unlock()
	return f.resolved
}

type hybridQUICRead struct {
	data   []byte
	target netip.AddrPort
	err    error
}

func newHybridQUICPacketConn(hy2 net.PacketConn, relay netip.AddrPort, newRaw, newFallback func() (net.PacketConn, error)) *hybridQUICPacketConn {
	c := &hybridQUICPacketConn{
		hy2:         hy2,
		relay:       relay,
		newRaw:      newRaw,
		newFallback: newFallback,
		flows:       make(map[hybridTarget]*hybridQUICFlow),
		flowsByID:   make(map[[16]byte]*hybridQUICFlow),
		readCh:      make(chan hybridQUICRead, 64),
		closed:      make(chan struct{}),
	}
	go c.readHY2()
	return c
}

func (c *hybridQUICPacketConn) WriteTo(payload []byte, destination net.Addr) (int, error) {
	target, ok := parseHybridDestination(destination)
	if !ok || !target.eligible() {
		return c.writeFallback(payload, destination)
	}

	c.mu.Lock()
	flow := c.flows[target]
	if flow == nil {
		if !isQUICInitial(payload) {
			// A flow only becomes eligible by starting with an Initial, which
			// is what carries the registration.
			c.mu.Unlock()
			return c.writeFallback(payload, destination)
		}
		var err error
		flow, err = c.newFlowLocked(target)
		if err != nil {
			c.mu.Unlock()
			return c.writeFallback(payload, destination)
		}
		c.flows[target] = flow
		c.flowsByID[flow.id] = flow
	}
	rejected := flow.rejected
	registered := flow.registered
	c.mu.Unlock()

	if rejected {
		return c.writeFallback(payload, destination)
	}

	if isQUICLongHeader(payload) {
		// The whole handshake goes over the tunnel, in this direction and the
		// other. A raw path that carried an Initial or a Handshake would show
		// an observer a QUIC connection that begins in the middle -- no
		// ClientHello ever sent on a tuple that is answering with one -- which
		// has no benign counterpart. A raw path that only ever carries 1-RTT
		// packets is shaped exactly like an ordinary connection migration,
		// which happens on any NAT rebinding.
		//
		// The first Initial registers the flow; everything after it is relayed
		// against the id that registration established.
		var control []byte
		if registered {
			control = flow.relayMessage(payload)
		} else {
			control = flow.initialMessage(payload)
		}
		if err := c.writeControl(control); err != nil {
			return 0, err
		}
		c.mu.Lock()
		flow.registered = true
		c.mu.Unlock()
		return len(payload), nil
	}

	if !registered {
		// Only before the flow has registered. Once its Initial has gone out
		// over the relay, the rest of the connection has to follow it: the
		// fallback is a separate tunnel session, and splitting one QUIC
		// connection across two of them shows the target two different source
		// endpoints and its handshake never completes.
		return c.writeFallback(payload, destination)
	}

	// A 1-RTT packet, and with it the handover. The raw path cannot be trusted
	// until something has come back on it: the server matches a raw packet by
	// the connection ID the target chose, and a target that rotates early or
	// uses a zero-length ID is one it will never match. So the flow sends on
	// both paths until the raw one answers, and gives up on it if none does.
	switch c.handoverPhase(flow) {
	case hybridPhaseRaw:
		return flow.raw.WriteTo(payload, net.UDPAddrFromAddrPort(c.relay))
	case hybridPhaseStuck:
		if err := c.writeControl(flow.relayMessage(payload)); err != nil {
			return 0, err
		}
		return len(payload), nil
	default:
		if err := c.writeControl(flow.relayMessage(payload)); err != nil {
			return 0, err
		}
		// A failure here is not fatal: the tunnel copy has already been sent,
		// so the packet is delivered either way.
		_, _ = flow.raw.WriteTo(payload, net.UDPAddrFromAddrPort(c.relay))
		return len(payload), nil
	}
}

// handoverPhase reports where the flow is in the move to the raw path, starting
// the handover on the first 1-RTT packet and abandoning it once the window has
// passed with nothing received.
func (c *hybridQUICPacketConn) handoverPhase(flow *hybridQUICFlow) hybridPhase {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch flow.phase {
	case hybridPhaseTunnel:
		flow.phase = hybridPhaseHandover
		flow.handoverExpiry = time.Now().Add(hybridHandoverTimeout)
	case hybridPhaseHandover:
		flow.handoverSent++
		if flow.handoverSent >= hybridHandoverPackets && time.Now().After(flow.handoverExpiry) {
			flow.phase = hybridPhaseStuck
			log.Debugln("[HY2] hybrid QUIC raw path never answered for %s, staying on the tunnel", flow.target)
		}
	}
	return flow.phase
}

func (c *hybridQUICPacketConn) writeControl(control []byte) error {
	n, err := c.hy2.WriteTo(control, hybridControlAddr{})
	if err != nil {
		return err
	}
	if n != len(control) {
		return errors.New("short hybrid QUIC control write")
	}
	return nil
}

func (c *hybridQUICPacketConn) writeFallback(payload []byte, destination net.Addr) (int, error) {
	c.mu.Lock()
	if c.fallback == nil {
		fallback, err := c.newFallback()
		if err != nil {
			c.mu.Unlock()
			return 0, err
		}
		c.fallback = fallback
		go c.readFallback(fallback)
	}
	fallback := c.fallback
	c.mu.Unlock()
	return fallback.WriteTo(payload, destination)
}

func (c *hybridQUICPacketConn) newFlowLocked(target hybridTarget) (*hybridQUICFlow, error) {
	raw, err := c.newRaw()
	if err != nil {
		return nil, err
	}
	flow := &hybridQUICFlow{owner: c, target: target, raw: raw}
	if _, err = rand.Read(flow.id[:]); err != nil {
		_ = raw.Close()
		return nil, err
	}
	go flow.readRaw()
	return flow, nil
}

func (f *hybridQUICFlow) initialMessage(payload []byte) []byte {
	target := f.target
	var addressed []byte
	switch {
	case target.isDomain():
		addressed = make([]byte, 0, 2+len(target.name))
		addressed = append(addressed, hybridTargetDomain, byte(len(target.name)))
		addressed = append(addressed, target.name...)
	case target.addr.Is4():
		addr := target.addr.As4()
		addressed = append([]byte{hybridTargetIPv4}, addr[:]...)
	default:
		addr := target.addr.As16()
		addressed = append([]byte{hybridTargetIPv6}, addr[:]...)
	}

	message := make([]byte, 0, 21+len(addressed)+2+len(payload))
	message = append(message, hybridQUICMagic...)
	message = append(message, hybridQUICInitial)
	message = append(message, f.id[:]...)
	message = append(message, addressed...)
	message = binary.BigEndian.AppendUint16(message, target.port)
	return append(message, payload...)
}

// relayMessage carries one packet of an already-registered flow over the
// tunnel. It names the flow by id alone: the destination was settled by the
// registration, and repeating it on every packet would only give a server the
// chance to be told two different ones.
func (f *hybridQUICFlow) relayMessage(payload []byte) []byte {
	message := make([]byte, 0, 21+len(payload))
	message = append(message, hybridQUICMagic...)
	message = append(message, hybridQUICRelay)
	message = append(message, f.id[:]...)
	return append(message, payload...)
}

// handleAck applies a registration result. Only a rejection changes anything:
// the server has no flow for this id, so raw packets would be dropped and the
// connection would stall on a path that will never answer.
func (c *hybridQUICPacketConn) handleAck(message []byte) bool {
	if len(message) < 22 || string(message[:4]) != hybridQUICMagic || message[4] != hybridQUICAck {
		return false
	}
	var id [16]byte
	copy(id[:], message[5:21])
	if message[21] != hybridAckOK {
		c.mu.Lock()
		if flow := c.flowsByID[id]; flow != nil {
			flow.rejected = true
		}
		c.mu.Unlock()
		return true
	}

	resolved, ok := parseHybridAckTarget(message[22:])
	c.mu.Lock()
	flow := c.flowsByID[id]
	if flow != nil && ok {
		// A name was registered, so the replies coming back on the raw path
		// have no address the client could have known. This is the one the
		// server actually opened the flow to.
		flow.resolved = resolved
	}
	c.mu.Unlock()
	if flow != nil {
		log.Debugln("[HY2] hybrid QUIC relay ready for %s --> %s", flow.target, resolved)
	}
	return true
}

// parseHybridAckTarget reads the address a successful registration resolved to.
// It is absent when the client registered a literal address, which it can label
// its own replies with.
func parseHybridAckTarget(tail []byte) (netip.AddrPort, bool) {
	if len(tail) < 1 {
		return netip.AddrPort{}, false
	}
	var addr netip.Addr
	switch tail[0] {
	case hybridTargetIPv4:
		if len(tail) < 1+4+2 {
			return netip.AddrPort{}, false
		}
		addr = netip.AddrFrom4([4]byte(tail[1:5]))
		tail = tail[5:]
	case hybridTargetIPv6:
		if len(tail) < 1+16+2 {
			return netip.AddrPort{}, false
		}
		addr = netip.AddrFrom16([16]byte(tail[1:17])).Unmap()
		tail = tail[17:]
	default:
		return netip.AddrPort{}, false
	}
	resolved := netip.AddrPortFrom(addr, binary.BigEndian.Uint16(tail[:2]))
	if !resolved.IsValid() {
		return netip.AddrPort{}, false
	}
	return resolved, true
}

func (f *hybridQUICFlow) readRaw() {
	buffer := make([]byte, 64*1024)
	for {
		n, sourceAddr, err := f.raw.ReadFrom(buffer)
		if err != nil {
			select {
			case <-f.owner.closed:
				return
			default:
			}
			f.owner.deliver(hybridQUICRead{err: err})
			return
		}
		source, ok := outboundUDPAddrPort(sourceAddr)
		if !ok {
			continue
		}
		if source != f.owner.relay {
			continue
		}
		// A name flow has no address of its own: under fake-IP the only one the
		// client holds is synthetic. The registration acknowledgement carries
		// the address the server resolved, which is also what its replies over
		// the tunnel were attributed to, so both paths label a datagram the
		// same way and the sender's mapping resolves either one.
		// Something came back on the raw path, so the server matched this
		// flow and the tunnel copy of each packet can stop.
		f.owner.mu.Lock()
		if f.phase != hybridPhaseRaw {
			f.phase = hybridPhaseRaw
			log.Debugln("[HY2] hybrid QUIC raw path live for %s", f.target)
		}
		f.owner.mu.Unlock()

		reported := f.reportAddr()
		data := append([]byte(nil), buffer[:n]...)
		f.owner.deliver(hybridQUICRead{data: data, target: reported})
	}
}

func (c *hybridQUICPacketConn) readHY2() {
	c.readPacketConn(c.hy2)
}

func (c *hybridQUICPacketConn) readFallback(fallback net.PacketConn) {
	c.readPacketConn(fallback)
}

func (c *hybridQUICPacketConn) readPacketConn(packetConn net.PacketConn) {
	buffer := make([]byte, 64*1024)
	for {
		n, source, err := packetConn.ReadFrom(buffer)
		if err != nil {
			select {
			case <-c.closed:
				return
			default:
			}
			c.deliver(hybridQUICRead{err: err})
			return
		}
		if source == nil || source.String() == hybridQUICControlAddress {
			if c.handleAck(buffer[:n]) {
				continue
			}
		}
		target, ok := outboundUDPAddrPort(source)
		if !ok {
			// The control address is a name, and the tunnel is under no
			// obligation to hand it back as one: a datagram addressed to a
			// name can return with no usable address at all. An
			// acknowledgement is identified by what it carries rather than
			// where it claims to be from, so it survives that.
			c.handleAck(buffer[:n])
			continue
		}
		c.deliver(hybridQUICRead{data: append([]byte(nil), buffer[:n]...), target: target})
	}
}

func (c *hybridQUICPacketConn) deliver(result hybridQUICRead) {
	select {
	case c.readCh <- result:
	case <-c.closed:
	}
}

func (c *hybridQUICPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case result := <-c.readCh:
		if result.err != nil {
			return 0, nil, result.err
		}
		if len(p) < len(result.data) {
			return 0, nil, errors.New("short hybrid QUIC read buffer")
		}
		return copy(p, result.data), net.UDPAddrFromAddrPort(result.target), nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	case <-c.readDeadline():
		return 0, nil, os.ErrDeadlineExceeded
	}
}

func (c *hybridQUICPacketConn) Close() error {
	var result error
	c.closeOne.Do(func() {
		close(c.closed)
		c.mu.Lock()
		for _, flow := range c.flows {
			if err := flow.raw.Close(); err != nil && result == nil {
				result = err
			}
		}
		if c.fallback != nil {
			if err := c.fallback.Close(); err != nil && result == nil {
				result = err
			}
		}
		c.mu.Unlock()
		if err := c.hy2.Close(); err != nil && result == nil {
			result = err
		}
	})
	return result
}

func (c *hybridQUICPacketConn) LocalAddr() net.Addr { return c.hy2.LocalAddr() }

func (c *hybridQUICPacketConn) SetDeadline(deadline time.Time) error {
	if err := c.SetReadDeadline(deadline); err != nil {
		return err
	}
	return c.SetWriteDeadline(deadline)
}

func (c *hybridQUICPacketConn) SetReadDeadline(deadline time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if c.readTimer != nil {
		c.readTimer.Stop()
		c.readTimer = nil
	}
	if !deadline.IsZero() {
		duration := max(time.Until(deadline), 0)
		c.readTimer = time.NewTimer(duration)
	}
	return nil
}

func (c *hybridQUICPacketConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, flow := range c.flows {
		if err := flow.raw.SetWriteDeadline(deadline); err != nil {
			return err
		}
	}
	if c.fallback != nil {
		if err := c.fallback.SetWriteDeadline(deadline); err != nil {
			return err
		}
	}
	return c.hy2.SetWriteDeadline(deadline)
}

func (c *hybridQUICPacketConn) readDeadline() <-chan time.Time {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if c.readTimer == nil {
		return nil
	}
	return c.readTimer.C
}

type hybridControlAddr struct{}

func (hybridControlAddr) Network() string { return "udp" }
func (hybridControlAddr) String() string  { return hybridQUICControlAddress }

// isQUICLongHeader reports whether this packet belongs to the handshake. Every
// one of them travels over the tunnel, so the raw path carries 1-RTT packets
// and nothing else.
func isQUICLongHeader(packet []byte) bool {
	if len(packet) < 5 || packet[0]&0xc0 != 0xc0 {
		return false
	}
	// A version of zero is Version Negotiation, which only a server sends.
	return binary.BigEndian.Uint32(packet[1:5]) != 0
}

func isQUICInitial(packet []byte) bool {
	if len(packet) < 5 || packet[0]&0xc0 != 0xc0 {
		return false
	}
	version := binary.BigEndian.Uint32(packet[1:5])
	packetType := (packet[0] >> 4) & 0x3
	switch version {
	case quicVersion1:
		return packetType == 0
	case quicVersion2:
		return packetType == 1
	default:
		return false
	}
}

func outboundUDPAddrPort(addr net.Addr) (netip.AddrPort, bool) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if ok && udpAddr != nil {
		addrPort := udpAddr.AddrPort()
		return netip.AddrPortFrom(addrPort.Addr().Unmap(), addrPort.Port()), addrPort.IsValid()
	}
	addrPort, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(addrPort.Addr().Unmap(), addrPort.Port()), true
}

func isHybridPublicTarget(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return false
	}
	// The default fake-IP pool is 198.18.0.1/16, which sits in the benchmarking
	// range rather than an RFC 1918 one: every test above passes it. A synthetic
	// address has no meaning to the relay, so it must never be registered as a
	// target.
	return !resolver.IsFakeIP(addr)
}

// hybridRelayAddr reports where the raw relay path must send, taken from the
// live tunnel rather than from a name lookup.
//
// The relay has to be the exact server the registration was just delivered to
// over that tunnel, and DNS cannot promise that. A second lookup of the server
// name can return a different host when it has several address records, and
// resolving it again through net.DefaultResolver would additionally hand the
// proxy server's name to the system resolver, which is the one place mihomo
// never sends it. Reading the connection also tracks port hopping for free:
// hopLoop rewrites the remote address in place.
func hybridRelayAddr(client *hysteria2.Client) (netip.AddrPort, error) {
	if client == nil {
		return netip.AddrPort{}, errors.New("hybrid QUIC has no client")
	}
	relay, ok := client.RemoteAddr()
	if !ok {
		return netip.AddrPort{}, errors.New("hybrid QUIC has no established tunnel")
	}
	if !isHybridPublicTarget(relay.Addr()) {
		return netip.AddrPort{}, errors.New("hybrid QUIC relay is not public")
	}
	return relay, nil
}
