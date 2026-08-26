//go:build android

package dns

import (
	"net"
	"net/netip"
	"sync"

	"github.com/metacubex/mihomo/component/resolver"
)

var (
	systemResolverMu sync.RWMutex
	systemResolver   []dnsClient
)

func FlushCacheWithDefaultResolver() {
	resolver.ClearCache()
	resolver.ResetConnection()
}

func UpdateSystemDNS(addr []string) {
	clients := make([]dnsClient, 0, len(addr))
	for _, d := range addr {
		if ip, err := netip.ParseAddr(d); err == nil {
			d = net.JoinHostPort(ip.String(), "53")
		} else if _, err := netip.ParseAddrPort(d); err != nil {
			continue
		}
		clients = append(clients, newSystemDNSClient(d, ""))
	}

	systemResolverMu.Lock()
	systemResolver = clients
	systemResolverMu.Unlock()
}

func (c *systemClient) getDnsClients() ([]dnsClient, error) {
	systemResolverMu.RLock()
	defer systemResolverMu.RUnlock()
	return append([]dnsClient(nil), systemResolver...), nil
}

func (c *systemClient) ResetConnection() {
	systemResolverMu.RLock()
	defer systemResolverMu.RUnlock()
	for _, r := range systemResolver {
		r.ResetConnection()
	}
}
