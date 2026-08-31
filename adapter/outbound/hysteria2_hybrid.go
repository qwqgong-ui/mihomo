package outbound

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"
	"github.com/miekg/dns"
)

const (
	hybridQUICControlAddress = "hybrid-quic.invalid:443"
	hybridQUICMagic          = "HQV1"
	hybridQUICInitial        = byte(1)
	quicVersion1             = uint32(1)
	quicVersion2             = uint32(0x6b3343cf)
)

var hybridQUICDNSAddress = netip.MustParseAddrPort("1.1.1.1:53")

const hybridTargetCacheMaxEntries = 256

type hybridTargetCacheEntry struct {
	addr      netip.Addr
	expiresAt time.Time
}

type hybridTargetCache struct {
	mu      sync.Mutex
	entries map[string]hybridTargetCacheEntry
	now     func() time.Time
}

func newHybridTargetCache() *hybridTargetCache {
	return &hybridTargetCache{
		entries: make(map[string]hybridTargetCacheEntry),
		now:     time.Now,
	}
}

func hybridTargetCacheKey(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func (c *hybridTargetCache) get(host string) (netip.Addr, bool) {
	if c == nil {
		return netip.Addr{}, false
	}
	key := hybridTargetCacheKey(host)
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, loaded := c.entries[key]
	if !loaded {
		return netip.Addr{}, false
	}
	if !now.Before(entry.expiresAt) {
		delete(c.entries, key)
		return netip.Addr{}, false
	}
	return entry.addr, true
}

func (c *hybridTargetCache) set(host string, addr netip.Addr, ttl time.Duration) {
	if c == nil || ttl <= 0 || !isHybridPublicTarget(addr) {
		return
	}
	key := hybridTargetCacheKey(host)
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= hybridTargetCacheMaxEntries {
		for existingKey, entry := range c.entries {
			if !now.Before(entry.expiresAt) {
				delete(c.entries, existingKey)
			}
		}
	}
	if len(c.entries) >= hybridTargetCacheMaxEntries {
		// The cache is deliberately small. Evict one arbitrary entry instead of
		// allowing untrusted destination names to grow it without bound.
		for existingKey := range c.entries {
			delete(c.entries, existingKey)
			break
		}
	}
	c.entries[key] = hybridTargetCacheEntry{addr: addr.Unmap(), expiresAt: now.Add(ttl)}
}

type hybridTargetQuery func(context.Context, string, uint16) (netip.Addr, time.Duration, error)

func resolveHybridTarget(ctx context.Context, host string, prefer C.DNSPrefer, cache *hybridTargetCache, query hybridTargetQuery) (netip.Addr, error) {
	if target, loaded := cache.get(host); loaded {
		return target, nil
	}

	// Dual stack favors IPv6 for hybrid QUIC. IPv4 remains the fallback when
	// the destination has no usable AAAA response.
	queryTypes := []uint16{dns.TypeAAAA, dns.TypeA}
	switch prefer {
	case C.IPv4Only:
		queryTypes = queryTypes[1:]
	case C.IPv6Only:
		queryTypes = queryTypes[:1]
	case C.IPv4Prefer:
		queryTypes[0], queryTypes[1] = queryTypes[1], queryTypes[0]
	}

	var queryErrors []error
	for _, queryType := range queryTypes {
		target, ttl, err := query(ctx, host, queryType)
		if err == nil && isHybridPublicTarget(target) {
			cache.set(host, target, ttl)
			return target, nil
		}
		queryErrors = append(queryErrors, err)
	}
	return netip.Addr{}, errors.Join(queryErrors...)
}

func (h *Hysteria2) resolveHybridTargetViaHY2(ctx context.Context, host string) (netip.Addr, error) {
	return resolveHybridTarget(ctx, host, h.prefer, h.hybridTargetCache, h.queryHybridTargetViaHY2)
}

func (h *Hysteria2) queryHybridTargetViaHY2(ctx context.Context, host string, queryType uint16) (netip.Addr, time.Duration, error) {
	pc, err := h.client.ListenPacket(ctx)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	deadline := time.Now().Add(3 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = pc.SetDeadline(deadline)

	request := new(dns.Msg)
	request.SetQuestion(dns.Fqdn(host), queryType)
	wire, err := request.Pack()
	if err != nil {
		_ = pc.Close()
		return netip.Addr{}, 0, err
	}
	if _, err = pc.WriteTo(wire, net.UDPAddrFromAddrPort(hybridQUICDNSAddress)); err != nil {
		_ = pc.Close()
		return netip.Addr{}, 0, err
	}
	responseWire := make([]byte, 4096)
	n, _, err := pc.ReadFrom(responseWire)
	_ = pc.Close()
	if err != nil {
		return netip.Addr{}, 0, err
	}
	response := new(dns.Msg)
	if err = response.Unpack(responseWire[:n]); err != nil {
		return netip.Addr{}, 0, err
	}
	if response.Id != request.Id || !response.Response || response.Rcode != dns.RcodeSuccess {
		return netip.Addr{}, 0, errors.New("invalid hybrid QUIC DNS response")
	}
	for _, answer := range response.Answer {
		var target netip.Addr
		switch record := answer.(type) {
		case *dns.A:
			if queryType == dns.TypeA {
				target, _ = netip.AddrFromSlice(record.A)
			}
		case *dns.AAAA:
			if queryType == dns.TypeAAAA {
				target, _ = netip.AddrFromSlice(record.AAAA)
			}
		}
		target = target.Unmap()
		if isHybridPublicTarget(target) {
			return target, time.Duration(answer.Header().Ttl) * time.Second, nil
		}
	}
	return netip.Addr{}, 0, errors.New("hybrid QUIC DNS response has no public address")
}

type hybridQUICPacketConn struct {
	hy2         net.PacketConn
	relay       netip.AddrPort
	newRaw      func() (net.PacketConn, error)
	newFallback func() (net.PacketConn, error)

	mu       sync.Mutex
	flows    map[netip.AddrPort]*hybridQUICFlow
	readCh   chan hybridQUICRead
	closed   chan struct{}
	closeOne sync.Once
	fallback net.PacketConn

	deadlineMu sync.Mutex
	readTimer  *time.Timer
}

type hybridQUICFlow struct {
	owner      *hybridQUICPacketConn
	id         [16]byte
	target     netip.AddrPort
	raw        net.PacketConn
	registered bool
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
		flows:       make(map[netip.AddrPort]*hybridQUICFlow),
		readCh:      make(chan hybridQUICRead, 64),
		closed:      make(chan struct{}),
	}
	go c.readHY2()
	return c
}

func (c *hybridQUICPacketConn) WriteTo(payload []byte, destination net.Addr) (int, error) {
	target, ok := outboundUDPAddrPort(destination)
	if !ok || target.Port() != 443 || !isHybridPublicTarget(target.Addr()) {
		return c.writeFallback(payload, destination)
	}

	c.mu.Lock()
	flow := c.flows[target]
	if flow == nil {
		var err error
		flow, err = c.newFlowLocked(target)
		if err != nil {
			c.mu.Unlock()
			return c.writeFallback(payload, destination)
		}
		c.flows[target] = flow
	}
	c.mu.Unlock()

	if isQUICInitial(payload) {
		control := flow.initialMessage(payload)
		n, err := c.hy2.WriteTo(control, hybridControlAddr{})
		if err != nil {
			return 0, err
		}
		if n != len(control) {
			return 0, errors.New("short hybrid QUIC control write")
		}
		c.mu.Lock()
		flow.registered = true
		c.mu.Unlock()
		return len(payload), nil
	}

	c.mu.Lock()
	registered := flow.registered
	c.mu.Unlock()
	if !registered {
		// A flow that did not begin with a recognizable Initial is not eligible
		// for raw relay. Preserve ordinary HY2 behavior instead of opening it.
		return c.writeFallback(payload, destination)
	}
	n, err := flow.raw.WriteTo(payload, net.UDPAddrFromAddrPort(c.relay))
	return n, err
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

func (c *hybridQUICPacketConn) newFlowLocked(target netip.AddrPort) (*hybridQUICFlow, error) {
	raw, err := c.newRaw()
	if err != nil {
		return nil, err
	}
	flow := &hybridQUICFlow{owner: c, target: target, raw: raw}
	if _, err = rand.Read(flow.id[:]); err != nil {
		_ = raw.Close()
		return nil, err
	}
	// Open the stateful IPv6 firewall path before the server sends the first
	// target response to this otherwise-silent raw socket. The relay drops this
	// non-Initial packet because the tuple is not registered yet.
	if n, probeErr := raw.WriteTo([]byte{0}, net.UDPAddrFromAddrPort(c.relay)); probeErr != nil || n != 1 {
		_ = raw.Close()
		if probeErr != nil {
			return nil, probeErr
		}
		return nil, errors.New("short hybrid QUIC raw probe write")
	}
	go flow.readRaw()
	return flow, nil
}

func (f *hybridQUICFlow) initialMessage(payload []byte) []byte {
	message := make([]byte, 42+len(payload))
	copy(message[:4], hybridQUICMagic)
	message[4] = hybridQUICInitial
	copy(message[5:21], f.id[:])
	local, _ := outboundUDPAddrPort(f.raw.LocalAddr())
	port := local.Port()
	binary.BigEndian.PutUint16(message[21:23], port)
	if f.target.Addr().Is4() {
		message[23] = 4
		addr := f.target.Addr().As4()
		copy(message[36:40], addr[:])
	} else {
		message[23] = 6
		addr := f.target.Addr().As16()
		copy(message[24:40], addr[:])
	}
	binary.BigEndian.PutUint16(message[40:42], f.target.Port())
	copy(message[42:], payload)
	return message
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
		data := append([]byte(nil), buffer[:n]...)
		f.owner.deliver(hybridQUICRead{data: data, target: f.target})
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
		target, ok := outboundUDPAddrPort(source)
		if !ok {
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

func isHybridPublicIPv6(addr netip.Addr) bool {
	return addr.Is6() && isHybridPublicTarget(addr)
}

func isHybridPublicTarget(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsValid() && addr.IsGlobalUnicast() && !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() && !addr.IsUnspecified()
}

func resolveHybridRelay(ctx context.Context, server string, port int) (netip.AddrPort, error) {
	if addr, err := netip.ParseAddr(server); err == nil {
		if !isHybridPublicIPv6(addr) {
			return netip.AddrPort{}, errors.New("hybrid QUIC relay is not public IPv6")
		}
		return netip.AddrPortFrom(addr, uint16(port)), nil
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip6", server)
	if err != nil {
		return netip.AddrPort{}, err
	}
	for _, addr := range addresses {
		if isHybridPublicIPv6(addr) {
			return netip.AddrPortFrom(addr, uint16(port)), nil
		}
	}
	return netip.AddrPort{}, errors.New("hybrid QUIC relay has no public IPv6")
}
