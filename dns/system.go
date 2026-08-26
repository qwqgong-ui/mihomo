package dns

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/resolver"

	D "github.com/miekg/dns"
)

const (
	SystemDnsFlushTime   = 5 * time.Minute
	SystemDnsDeleteTimes = 12 // 12*5 = 60min
)

type systemDnsClient struct {
	disableTimes uint32
	dnsClient
}

type systemNameServer struct {
	address       string
	interfaceName string
}

func (s systemNameServer) key() string {
	return s.interfaceName + "\x00" + s.address
}

func defaultPhysicalInterface() string {
	if finder := dialer.SystemDNSInterfaceFinder.Load(); finder != nil {
		for _, destination := range [...]string{"1.1.1.1", "2606:4700:4700::1111"} {
			if interfaceName := finder.FindInterfaceName(netip.MustParseAddr(destination)); interfaceName != "" {
				return interfaceName
			}
		}
	}
	if interfaceName := dialer.DefaultInterface.Load(); interfaceName != "" {
		return interfaceName
	}
	if finder := dialer.DefaultInterfaceFinder.Load(); finder != nil {
		// Use stable public destinations only to ask the route/interface monitor;
		// no packet is sent to either address.
		for _, destination := range [...]string{"1.1.1.1", "2606:4700:4700::1111"} {
			if interfaceName := finder.FindInterfaceName(netip.MustParseAddr(destination)); interfaceName != "" {
				return interfaceName
			}
		}
	}
	return ""
}

type systemClient struct {
	mu         sync.Mutex
	dnsClients map[string]*systemDnsClient
	lastFlush  time.Time
	defaultNS  []dnsClient
}

func (c *systemClient) ExchangeContext(ctx context.Context, m *D.Msg) (msg *D.Msg, err error) {
	dnsClients, err := c.getDnsClients()
	if len(dnsClients) == 0 && len(c.defaultNS) > 0 {
		dnsClients = c.defaultNS
		err = nil
	}
	if err != nil {
		return
	}
	msg, _, err = batchExchange(ctx, dnsClients, m)
	return
}

// Address implements dnsClient
func (c *systemClient) Address() string {
	dnsClients, _ := c.getDnsClients()
	isDefault := ""
	if len(dnsClients) == 0 && len(c.defaultNS) > 0 {
		dnsClients = c.defaultNS
		isDefault = "[defaultNS]"
	}
	addrs := make([]string, 0, len(dnsClients))
	for _, c := range dnsClients {
		addrs = append(addrs, c.Address())
	}
	return fmt.Sprintf("system%s(%s)", isDefault, strings.Join(addrs, ","))
}

var _ dnsClient = (*systemClient)(nil)

func newSystemClient() *systemClient {
	return &systemClient{
		dnsClients: map[string]*systemDnsClient{},
	}
}

func init() {
	r := NewResolver(Config{})
	c := newSystemClient()
	c.defaultNS = transform([]NameServer{{Addr: "114.114.114.114:53"}, {Addr: "8.8.8.8:53"}}, nil)
	r.main = []dnsClient{c}
	resolver.SystemResolver = r
}
