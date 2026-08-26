package ecs

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/metacubex/mihomo/component/dialer"

	D "github.com/miekg/dns"
)

// whoamiTimeout bounds one probe. Every probe of a family runs in parallel,
// so this is the whole wait, not a per-server one.
const whoamiTimeout = 4 * time.Second

// whoamiProbes ask a server that answers with the source address it sees.
// They are plain DNS over UDP/53 against fixed IPs, which needs no name
// resolution to bootstrap and survives the networks that filter the STUN
// ports — the common case being an ISP that drops UDP/3478 outright.
var whoamiProbes = []whoamiProbe{
	{
		question: "myip.opendns.com.",
		v4Server: netip.MustParseAddrPort("208.67.222.222:53"),
		v6Server: netip.MustParseAddrPort("[2620:119:35::35]:53"),
	},
	{
		question: "o-o.myaddr.l.google.com.",
		txt:      true,
		v4Server: netip.MustParseAddrPort("216.239.32.10:53"),
		v6Server: netip.MustParseAddrPort("[2001:4860:4802:32::a]:53"),
	},
	{
		question: "whoami.akamai.net.",
		v4Server: netip.MustParseAddrPort("193.108.88.1:53"), // IPv4 only
	},
}

type whoamiProbe struct {
	question string
	// txt marks the servers that carry the address in a TXT record instead
	// of an address record.
	txt      bool
	v4Server netip.AddrPort
	v6Server netip.AddrPort
}

// discoverWhoami races every probe that can serve this family and returns the
// first address that checks out.
func discoverWhoami(ctx context.Context, ipv4 bool, whoamiProbes []whoamiProbe) (netip.Addr, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		addr netip.Addr
		err  error
	}
	results := make(chan result, len(whoamiProbes))
	pending := 0
	for _, probe := range whoamiProbes {
		if !probe.server(ipv4).IsValid() {
			continue // this probe does not serve the family
		}
		probe := probe // go.mod targets go1.20, the loop variable is shared
		pending++
		go func() {
			addr, err := probe.exchange(ctx, ipv4)
			results <- result{addr, err}
		}()
	}
	if pending == 0 {
		return netip.Addr{}, errNoWhoamiServer
	}

	var errs []error
	for i := 0; i < pending; i++ {
		got := <-results
		if got.err == nil {
			return got.addr, nil
		}
		errs = append(errs, got.err)
	}
	return netip.Addr{}, errors.Join(errs...)
}

func (p whoamiProbe) server(ipv4 bool) netip.AddrPort {
	if ipv4 {
		return p.v4Server
	}
	return p.v6Server
}

func (p whoamiProbe) exchange(ctx context.Context, ipv4 bool) (netip.Addr, error) {
	qType := D.TypeA
	network := "udp4"
	if !ipv4 {
		qType, network = D.TypeAAAA, "udp6"
	}
	if p.txt {
		qType = D.TypeTXT
	}
	query := new(D.Msg)
	query.SetQuestion(p.question, qType)
	packed, err := query.Pack()
	if err != nil {
		return netip.Addr{}, err
	}

	server := p.server(ipv4)
	conn, err := dialer.ListenPacket(ctx, network, "", server)
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() { // unblock the read when the racing probe wins
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	deadline := time.Now().Add(whoamiTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return netip.Addr{}, err
	}
	if _, err = conn.WriteTo(packed, net.UDPAddrFromAddrPort(server)); err != nil {
		return netip.Addr{}, err
	}

	buffer := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buffer)
		if err != nil {
			return netip.Addr{}, err
		}
		reply := new(D.Msg)
		if err = reply.Unpack(buffer[:n]); err != nil || reply.Id != query.Id {
			continue // a stray datagram; keep waiting until the deadline
		}
		if addr, ok := p.parse(reply, ipv4); ok {
			return addr, nil
		}
		return netip.Addr{}, errNoPublicAddress
	}
}

func (p whoamiProbe) parse(reply *D.Msg, ipv4 bool) (netip.Addr, bool) {
	for _, answer := range reply.Answer {
		var addr netip.Addr
		switch record := answer.(type) {
		case *D.A:
			addr, _ = netip.AddrFromSlice(record.A)
		case *D.AAAA:
			addr, _ = netip.AddrFromSlice(record.AAAA)
		case *D.TXT:
			addr, _ = netip.ParseAddr(strings.TrimSpace(strings.Join(record.Txt, "")))
		default:
			continue
		}
		if addr = addr.Unmap(); acceptAddr(addr, ipv4) {
			return addr, true
		}
	}
	return netip.Addr{}, false
}

var errNoWhoamiServer = errors.New("no whoami server for this family")
