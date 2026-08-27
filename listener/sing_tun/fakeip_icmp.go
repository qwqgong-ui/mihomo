package sing_tun

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/component/directrace"
	"github.com/metacubex/mihomo/component/resolver"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"

	tun "github.com/metacubex/sing-tun"
	"github.com/metacubex/sing-tun/ping"
	"github.com/metacubex/sing/common/buf"
	M "github.com/metacubex/sing/common/metadata"
)

const (
	maxFakeIPICMPCandidates = 8
	maxFakeIPICMPRequests   = 1024
)

type icmpDirectResolver interface {
	ResolveICMPDirect(metadata *C.Metadata) C.ProxyAdapter
}

type icmpDirectController interface {
	Name() string
	ICMPControl(destination netip.Addr) func(string, string, syscall.RawConn) error
}

type fakeIPICMPRequest struct {
	identifier uint16
	sequence   uint16
}

type fakeIPICMPRace struct {
	mu           sync.Mutex
	closed       atomic.Bool
	fakeIP       netip.Addr
	host         string
	adapter      string
	backWriter   tun.DirectRouteContext
	routeContext tun.DirectRouteContext
	destinations map[netip.Addr]tun.DirectRouteDestination
	winners      map[uint16]netip.Addr
	requests     map[fakeIPICMPRequest][]byte
	reporters    map[fakeIPICMPRequest]netip.Addr
}

type fakeIPICMPCandidateContext struct {
	race      *fakeIPICMPRace
	candidate netip.Addr
}

func (h *ListenerHandler) prepareFakeIPICMPRace(source, destination M.Socksaddr, routeContext tun.DirectRouteContext, timeout time.Duration) (tun.DirectRouteDestination, error) {
	host, loaded := resolver.FindHostByIP(destination.Addr)
	if !loaded || host == "" {
		log.Infoln("[ICMP] fake IP %s has no host mapping, using fake ping echo", destination.Addr)
		return nil, nil
	}
	routeResolver, ok := h.Tunnel.(icmpDirectResolver)
	if !ok {
		return nil, nil
	}
	metadata := &C.Metadata{
		NetWork: C.UDP,
		Type:    C.TUN,
		SrcIP:   source.Addr,
		Host:    host,
	}
	adapter := routeResolver.ResolveICMPDirect(metadata)
	controller, ok := adapter.(icmpDirectController)
	if !ok || adapter.Type() != C.Direct {
		log.Debugln("[ICMP] fake IP %s (%s) is not routed to DIRECT, using fake ping echo", destination.Addr, host)
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var candidates []netip.Addr
	var err error
	if destination.Addr.Is6() {
		candidates, err = resolver.LookupIPv6WithResolver(ctx, host, resolver.DirectHostResolver)
	} else {
		candidates, err = resolver.LookupIPv4WithResolver(ctx, host, resolver.DirectHostResolver)
	}
	if err != nil || len(candidates) == 0 {
		// The Fake-IP answers for a name nothing can resolve - a typo, a dead
		// domain, a URL someone handed to ping. Real traffic to it fails when
		// DIRECT resolves the name at dial time, so a synthesized echo reply here
		// would report a host as up that nothing can actually reach. Report it
		// unreachable instead; nothing is cached, so the next echo request tries
		// the resolver again and a transient failure heals itself.
		log.Infoln("[ICMP] fake IP %s (%s) does not resolve, reporting unreachable: %v", destination.Addr, host, err)
		return nil, tun.ErrReset
	}
	candidates = uniqueFakeIPICMPCandidates(candidates)
	if len(candidates) > maxFakeIPICMPCandidates {
		candidates = candidates[:maxFakeIPICMPCandidates]
	}

	race := &fakeIPICMPRace{
		fakeIP:       destination.Addr,
		host:         host,
		adapter:      controller.Name(),
		backWriter:   ping.NewContextDestinationWriter(routeContext, destination.Addr),
		routeContext: routeContext,
		destinations: make(map[netip.Addr]tun.DirectRouteDestination, len(candidates)),
		winners:      make(map[uint16]netip.Addr),
		requests:     make(map[fakeIPICMPRequest][]byte),
		reporters:    make(map[fakeIPICMPRequest]netip.Addr),
	}
	var connectErrs []error
	for _, candidate := range candidates {
		candidate = candidate.Unmap()
		candidateContext := &fakeIPICMPCandidateContext{race: race, candidate: candidate}
		destination, connectErr := ping.ConnectDestination(context.Background(), log.SingLogger, controller.ICMPControl(candidate), candidate, candidateContext, timeout)
		if connectErr != nil {
			connectErrs = append(connectErrs, connectErr)
			continue
		}
		race.destinations[candidate] = ping.NewDestinationWriter(destination, candidate)
	}
	if len(race.destinations) == 0 {
		// Same reasoning: no candidate could even be opened, so the Fake-IP is
		// unreachable rather than pingable. A bare error would leave the stack
		// synthesizing an echo reply, which is the answer we are trying not to give.
		log.Warnln("[ICMP] fake IP %s (%s) has no reachable candidate: %v", destination.Addr, host, errors.Join(connectErrs...))
		return nil, tun.ErrReset
	}
	log.Infoln("[ICMP] %s %s --> %s (%s) racing %d DIRECT candidates", "icmp", source, destination.Addr, host, len(race.destinations))
	return race, nil
}

func uniqueFakeIPICMPCandidates(candidates []netip.Addr) []netip.Addr {
	seen := make(map[netip.Addr]struct{}, len(candidates))
	unique := make([]netip.Addr, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = candidate.Unmap()
		if !candidate.IsValid() {
			continue
		}
		if _, loaded := seen[candidate]; loaded {
			continue
		}
		seen[candidate] = struct{}{}
		unique = append(unique, candidate)
	}
	return unique
}

func (r *fakeIPICMPRace) WritePacket(packet *buf.Buffer) error {
	identifier, sequence, payload, ok := parseFakeIPICMPPacket(packet.Bytes(), false, r.fakeIP.Is6())
	if !ok {
		packet.Release()
		return errors.New("invalid ICMP echo request")
	}
	request := fakeIPICMPRequest{identifier: identifier, sequence: sequence}

	r.mu.Lock()
	if len(r.requests) >= maxFakeIPICMPRequests {
		for oldRequest := range r.requests {
			delete(r.requests, oldRequest)
			break
		}
	}
	r.requests[request] = bytes.Clone(payload)
	winner := r.winners[identifier]
	var destinations []tun.DirectRouteDestination
	if winner.IsValid() {
		if destination := r.destinations[winner]; destination != nil {
			destinations = append(destinations, destination)
		}
	} else {
		for _, destination := range r.destinations {
			destinations = append(destinations, destination)
		}
	}
	r.mu.Unlock()

	if len(destinations) == 0 {
		packet.Release()
		return errors.New("ICMP race has no live destination")
	}
	var errs []error
	for index, destination := range destinations {
		outboundPacket := packet
		if index != len(destinations)-1 {
			outboundPacket = packet.ToOwned()
		}
		if err := destination.WritePacket(outboundPacket); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *fakeIPICMPCandidateContext) WritePacket(packet []byte) error {
	return c.race.handleReply(c.candidate, packet)
}

func (r *fakeIPICMPRace) handleReply(candidate netip.Addr, packet []byte) error {
	identifier, sequence, payload, ok := parseFakeIPICMPPacket(packet, true, r.fakeIP.Is6())
	if !ok {
		return r.handleError(candidate, packet)
	}
	request := fakeIPICMPRequest{identifier: identifier, sequence: sequence}
	r.mu.Lock()
	expectedPayload, loaded := r.requests[request]
	if !loaded || !bytes.Equal(payload, expectedPayload) {
		r.mu.Unlock()
		return nil
	}
	winner := r.winners[identifier]
	if winner.IsValid() && winner != candidate {
		r.mu.Unlock()
		return nil
	}
	if !winner.IsValid() {
		r.winners[identifier] = candidate
		directrace.Store(r.host, r.adapter, candidate)
		log.Debugln("[ICMP] fake IP %s (%s) winner %s", r.fakeIP, r.host, candidate)
	}
	delete(r.requests, request)
	r.mu.Unlock()

	// backWriter rewrites the real source to the original Fake-IP and updates
	// IPv4/ICMPv6 checksums before the packet re-enters TUN. The application
	// must never observe the real candidate address.
	return r.backWriter.WritePacket(packet)
}

// handleError relays an ICMP error (Time Exceeded, Destination Unreachable,
// Packet Too Big) that a router on the way to a racing candidate reported.
// sing-tun has already matched it against the candidate's outstanding requests
// and restored the identifier and source address in the embedded datagram; what
// is left is the embedded destination, which still names the real candidate and
// would tell the application the Fake-IP it pinged is not the address it is
// talking to. The outer source stays the reporting router - that address is the
// whole point of a traceroute hop - so this cannot go through backWriter.
func (r *fakeIPICMPRace) handleError(candidate netip.Addr, packet []byte) error {
	message, ok := parseFakeIPICMPError(packet, r.fakeIP.Is6())
	if !ok {
		return nil
	}
	request := fakeIPICMPRequest{identifier: message.identifier, sequence: message.sequence}

	r.mu.Lock()
	expectedPayload, loaded := r.requests[request]
	if !loaded || !bytes.HasPrefix(expectedPayload, message.payload) {
		r.mu.Unlock()
		return nil
	}
	if winner := r.winners[message.identifier]; winner.IsValid() && winner != candidate {
		r.mu.Unlock()
		return nil
	}
	// Every candidate still in the race gets the same probe, so a low TTL comes
	// back once per candidate. Report the hop that answered first and drop the
	// rest, or the application prints one line per candidate for a single hop.
	reporter, reported := r.reporters[request]
	if reported && reporter != candidate {
		r.mu.Unlock()
		return nil
	}
	if !reported {
		if len(r.reporters) >= maxFakeIPICMPRequests {
			for oldRequest := range r.reporters {
				delete(r.reporters, oldRequest)
				break
			}
		}
		r.reporters[request] = candidate
	}
	r.mu.Unlock()

	rewriteFakeIPICMPErrorDestination(packet, message, r.fakeIP)
	log.Debugln("[ICMP] fake IP %s (%s) error type %d via %s seq %d", r.fakeIP, r.host, message.icmpType, candidate, message.sequence)
	return r.routeContext.WritePacket(packet)
}

type fakeIPICMPError struct {
	identifier            uint16
	sequence              uint16
	icmpType              byte
	payload               []byte
	icmpChecksumOffset    int
	innerDestinationStart int
	innerChecksumOffset   int
}

// parseFakeIPICMPError locates the echo request embedded in an ICMP error and
// the fields that have to move with the embedded destination address.
func parseFakeIPICMPError(packet []byte, ipv6 bool) (message fakeIPICMPError, ok bool) {
	if !ipv6 {
		if len(packet) < 20 || packet[0]>>4 != 4 {
			return
		}
		headerLength := int(packet[0]&0x0f) * 4
		if headerLength < 20 || len(packet) < headerLength+8 {
			return
		}
		icmpType := packet[headerLength]
		if icmpType != 3 && icmpType != 11 {
			return
		}
		inner := packet[headerLength+8:]
		if len(inner) < 20 || inner[0]>>4 != 4 || inner[9] != 1 {
			return
		}
		innerHeaderLength := int(inner[0]&0x0f) * 4
		if innerHeaderLength < 20 || len(inner) < innerHeaderLength+8 {
			return
		}
		innerICMP := inner[innerHeaderLength:]
		if innerICMP[0] != 8 || innerICMP[1] != 0 {
			return
		}
		message.icmpType = icmpType
		message.identifier = binary.BigEndian.Uint16(innerICMP[4:6])
		message.sequence = binary.BigEndian.Uint16(innerICMP[6:8])
		message.payload = innerICMP[8:]
		message.icmpChecksumOffset = headerLength + 2
		message.innerDestinationStart = headerLength + 8 + 16
		message.innerChecksumOffset = headerLength + 8 + 10
		ok = true
		return
	}
	if len(packet) < 40 || packet[0]>>4 != 6 || packet[6] != 58 || len(packet) < 48 {
		return
	}
	icmpType := packet[40]
	if icmpType != 1 && icmpType != 2 && icmpType != 3 && icmpType != 4 {
		return
	}
	inner := packet[48:]
	if len(inner) < 48 || inner[0]>>4 != 6 || inner[6] != 58 {
		return
	}
	innerICMP := inner[40:]
	if innerICMP[0] != 128 || innerICMP[1] != 0 {
		return
	}
	message.icmpType = icmpType
	message.identifier = binary.BigEndian.Uint16(innerICMP[4:6])
	message.sequence = binary.BigEndian.Uint16(innerICMP[6:8])
	message.payload = innerICMP[8:]
	message.icmpChecksumOffset = 42
	message.innerDestinationStart = 48 + 24
	message.innerChecksumOffset = 48 + 40 + 2
	ok = true
	return
}

// rewriteFakeIPICMPErrorDestination puts the Fake-IP back in the embedded
// datagram. The embedded checksum and the enclosing ICMP checksum both cover
// the address, and the enclosing one also covers the embedded checksum, so all
// three are updated incrementally per RFC 1624 eqn.3 - the embedded transport
// checksum cannot be recomputed anyway, since routers are only required to
// return the first bytes of the datagram they dropped.
func rewriteFakeIPICMPErrorDestination(packet []byte, message fakeIPICMPError, fakeIP netip.Addr) {
	addressLength := 4
	if fakeIP.Is6() {
		addressLength = 16
	}
	destination := packet[message.innerDestinationStart : message.innerDestinationStart+addressLength]
	oldDestination := bytes.Clone(destination)
	newDestination := fakeIP.AsSlice()
	if bytes.Equal(oldDestination, newDestination) {
		return
	}
	innerChecksum := packet[message.innerChecksumOffset : message.innerChecksumOffset+2]
	oldInnerChecksum := bytes.Clone(innerChecksum)
	copy(destination, newDestination)
	binary.BigEndian.PutUint16(innerChecksum, updateOnesComplementChecksum(
		binary.BigEndian.Uint16(oldInnerChecksum), oldDestination, newDestination))
	icmpChecksum := packet[message.icmpChecksumOffset : message.icmpChecksumOffset+2]
	binary.BigEndian.PutUint16(icmpChecksum, updateOnesComplementChecksum(
		binary.BigEndian.Uint16(icmpChecksum),
		append(oldDestination, oldInnerChecksum...),
		append(newDestination, innerChecksum...)))
}

// updateOnesComplementChecksum implements HC' = ~(~HC + ~m + m') from RFC 1624
// eqn.3, for a change of an even number of bytes at a 16-bit aligned offset.
func updateOnesComplementChecksum(checksum uint16, old, new []byte) uint16 {
	sum := uint32(^checksum)
	for index := 0; index+1 < len(old); index += 2 {
		sum += uint32(^binary.BigEndian.Uint16(old[index : index+2]))
	}
	for index := 0; index+1 < len(new); index += 2 {
		sum += uint32(binary.BigEndian.Uint16(new[index : index+2]))
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

func parseFakeIPICMPPacket(packet []byte, reply, ipv6 bool) (identifier, sequence uint16, payload []byte, ok bool) {
	headerLength := 40
	expectedType := byte(128)
	if reply {
		expectedType = 129
	}
	if !ipv6 {
		if len(packet) < 20 || packet[0]>>4 != 4 {
			return
		}
		headerLength = int(packet[0]&0x0f) * 4
		expectedType = 8
		if reply {
			expectedType = 0
		}
	} else if len(packet) < 40 || packet[0]>>4 != 6 {
		return
	}
	if headerLength < 20 || len(packet) < headerLength+8 || packet[headerLength] != expectedType || packet[headerLength+1] != 0 {
		return
	}
	identifier = binary.BigEndian.Uint16(packet[headerLength+4 : headerLength+6])
	sequence = binary.BigEndian.Uint16(packet[headerLength+6 : headerLength+8])
	payload = packet[headerLength+8:]
	ok = true
	return
}

func (r *fakeIPICMPRace) Close() error {
	if !r.closed.CompareAndSwap(false, true) {
		return nil
	}
	r.mu.Lock()
	destinations := make([]tun.DirectRouteDestination, 0, len(r.destinations))
	for _, destination := range r.destinations {
		destinations = append(destinations, destination)
	}
	r.mu.Unlock()
	var errs []error
	for _, destination := range destinations {
		errs = append(errs, destination.Close())
	}
	return errors.Join(errs...)
}

func (r *fakeIPICMPRace) IsClosed() bool {
	return r.closed.Load()
}
