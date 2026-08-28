package dialer

import (
	"net"
	"net/netip"
	"sort"
	"strings"

	"github.com/metacubex/mihomo/common/atomic"
)

var directNetworkEnvironment = atomic.NewTypedValue[string]("")

// SetDirectNetworkEnvironment supplies a stable platform-owned network
// identity when the Go process cannot discover the physical path itself. The
// Android VPN wrapper uses a privacy-preserving Wi-Fi/SIM fingerprint.
func SetDirectNetworkEnvironment(environment string) {
	directNetworkEnvironment.Store(strings.TrimSpace(environment))
}

func directNetworkScope(opt option) string {
	if environment := directNetworkEnvironment.Load(); environment != "" {
		return "environment|" + environment
	}
	interfaceName := opt.interfaceName
	if interfaceName == "" {
		interfaceName = DefaultInterface.Load()
	}
	if interfaceName == "" {
		if finder := DefaultInterfaceFinder.Load(); finder != nil {
			interfaceName = finder.FindInterfaceName(netip.MustParseAddr("1.1.1.1"))
		}
	}
	if interfaceName == "" {
		return "default"
	}

	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return interfaceName
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return interfaceName
	}
	prefixes := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err != nil {
			continue
		}
		prefixes = append(prefixes, prefix)
	}
	return scopeForPrefixes(interfaceName, prefixes)
}

func scopeForPrefixes(interfaceName string, prefixes []netip.Prefix) string {
	parts := make([]string, 0, len(prefixes))
	private192 := netip.MustParsePrefix("192.168.0.0/16")
	for _, prefix := range prefixes {
		addr := prefix.Addr().Unmap()
		if !addr.IsValid() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
			continue
		}
		if addr.Is4() && private192.Contains(addr) {
			prefix = netip.PrefixFrom(addr, 16)
		} else if addr != prefix.Addr() {
			prefix = netip.PrefixFrom(addr, min(prefix.Bits(), addr.BitLen()))
		}
		parts = append(parts, prefix.Masked().String())
	}
	sort.Strings(parts)
	parts = compactStrings(parts)
	if len(parts) == 0 {
		return interfaceName
	}
	return interfaceName + "|" + strings.Join(parts, ",")
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	write := 1
	for read := 1; read < len(values); read++ {
		if values[read] == values[write-1] {
			continue
		}
		values[write] = values[read]
		write++
	}
	return values[:write]
}
