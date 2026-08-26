//go:build !android

package dns

import (
	"net"
	"time"

	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/log"

	"golang.org/x/exp/slices"
)

func (c *systemClient) getDnsClients() ([]dnsClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	if time.Since(c.lastFlush) > SystemDnsFlushTime {
		var nameservers []systemNameServer
		if nameservers, err = dnsReadConfig(); err == nil {
			log.Debugln("[DNS] system dns update to %s", nameservers)
			for _, nameserver := range nameservers {
				if resolver.IsSystemDnsBlacklisted(nameserver.address) {
					continue
				}
				key := nameserver.key()
				if _, ok := c.dnsClients[key]; !ok {
					c.dnsClients[key] = &systemDnsClient{
						disableTimes: 0,
						dnsClient: newSystemDNSClient(
							net.JoinHostPort(nameserver.address, "53"),
							nameserver.interfaceName,
						),
					}
				}
			}
			available := 0
			for key, sdc := range c.dnsClients {
				if slices.ContainsFunc(nameservers, func(nameserver systemNameServer) bool { return nameserver.key() == key }) {
					sdc.disableTimes = 0 // enable
					available++
				} else {
					if sdc.disableTimes > SystemDnsDeleteTimes {
						delete(c.dnsClients, key) // drop too old dnsClient
					} else {
						sdc.disableTimes++
					}
				}
			}
			if available > 0 {
				c.lastFlush = time.Now()
			}
		}
	}
	dnsClients := make([]dnsClient, 0, len(c.dnsClients))
	for _, sdc := range c.dnsClients {
		if sdc.disableTimes == 0 {
			dnsClients = append(dnsClients, sdc.dnsClient)
		}
	}
	if len(dnsClients) > 0 {
		return dnsClients, nil
	}
	return nil, err
}

func (c *systemClient) ResetConnection() {}
