package ecs

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"

	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/resolver"

	"github.com/metacubex/sing-quic/hysteria2/realm"
)

const defaultSTUNPort = "3478"

// stunServers are contacted over the direct egress, so servers that stay
// reachable without a proxy come first.
var stunServers = []string{
	"stun.miwifi.com:3478",
	"stun.chat.bilibili.com:3478",
	"stun.qq.com:3478",
	"stun.cloudflare.com:3478",
}

// discoverSTUN asks the STUN servers what source address they see, which is
// the public address of this egress.
func discoverSTUN(ctx context.Context, ipv4 bool, stunServers []string) (netip.Addr, error) {
	servers, err := resolveSTUNServers(ctx, ipv4, stunServers)
	if err != nil {
		return netip.Addr{}, err
	}

	network := "udp4"
	if !ipv4 {
		network = "udp6"
	}
	conn, err := dialer.ListenPacket(ctx, network, "", servers[0])
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()

	addresses, err := realm.Discover(ctx, conn, servers)
	if err != nil {
		return netip.Addr{}, err
	}
	for _, address := range addresses {
		if addr := address.Addr().Unmap(); acceptAddr(addr, ipv4) {
			return addr, nil
		}
	}
	return netip.Addr{}, errNoPublicAddress
}

// resolveSTUNServers keeps every server it can resolve for this family and
// drops the rest, so one unresolvable host cannot fail the whole round.
func resolveSTUNServers(ctx context.Context, ipv4 bool, stunServers []string) ([]netip.AddrPort, error) {
	var resolved []netip.AddrPort
	var errs []error
	for _, server := range stunServers {
		host, port, err := net.SplitHostPort(server)
		if err != nil {
			host, port = server, defaultSTUNPort
		}
		portNumber, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if addr, err := netip.ParseAddr(host); err == nil {
			if addr.Unmap().Is4() == ipv4 {
				resolved = append(resolved, netip.AddrPortFrom(addr.Unmap(), uint16(portNumber)))
			}
			continue
		}
		addresses, err := lookupSTUNHost(ctx, host, ipv4)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, addr := range addresses {
			resolved = append(resolved, netip.AddrPortFrom(addr.Unmap(), uint16(portNumber)))
		}
	}
	if len(resolved) == 0 {
		return nil, errors.Join(append(errs, errNoSTUNServer)...)
	}
	return resolved, nil
}

// lookupSTUNHost resolves a STUN server name through the bootstrap
// (`default-nameserver`) resolver: it is the one list that must be plain IP
// servers, so it answers regardless of how the other lists are set up — a
// `nameserver` of `rcode://success` answers nothing by design, and the direct
// nameservers are the very clients waiting on this result. A nil bootstrap
// resolver makes [resolver.LookupIPv4WithResolver] fall back to the system
// one, and a miss just leaves the prefix invalid.
func lookupSTUNHost(ctx context.Context, host string, ipv4 bool) ([]netip.Addr, error) {
	if ipv4 {
		return resolver.LookupIPv4WithResolver(ctx, host, resolver.BootstrapResolver)
	}
	return resolver.LookupIPv6WithResolver(ctx, host, resolver.BootstrapResolver)
}

var errNoSTUNServer = errors.New("no STUN server resolved")
