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
	destinations map[netip.Addr]tun.DirectRouteDestination
	winners      map[uint16]netip.Addr
	requests     map[fakeIPICMPRequest][]byte
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
		log.Debugln("[ICMP] resolve fake IP %s (%s) failed: %v", destination.Addr, host, err)
		return nil, nil
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
		destinations: make(map[netip.Addr]tun.DirectRouteDestination, len(candidates)),
		winners:      make(map[uint16]netip.Addr),
		requests:     make(map[fakeIPICMPRequest][]byte),
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
		return nil, errors.Join(connectErrs...)
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
		return nil
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
