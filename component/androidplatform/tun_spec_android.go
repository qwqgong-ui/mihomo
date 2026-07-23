//go:build android

package androidplatform

import (
	"net/netip"
	"sort"

	LC "github.com/metacubex/mihomo/listener/config"
)

func makeTunSpec(tunConfig LC.Tun, dnsEnabled bool) tunSpec {
	mtu := tunConfig.MTU
	if mtu == 0 {
		mtu = 9000
	}

	routes := append([]netip.Prefix{}, tunConfig.RouteAddress...)
	routes = append(routes, tunConfig.Inet4RouteAddress...)
	routes = append(routes, tunConfig.Inet6RouteAddress...)
	excludedRoutes := append([]netip.Prefix{}, tunConfig.RouteExcludeAddress...)
	excludedRoutes = append(excludedRoutes, tunConfig.Inet4RouteExcludeAddress...)
	excludedRoutes = append(excludedRoutes, tunConfig.Inet6RouteExcludeAddress...)

	inet4Routes, inet6Routes := splitPrefixes(routes)
	inet4Excluded, inet6Excluded := splitPrefixes(excludedRoutes)
	return tunSpec{
		MTU:                      mtu,
		Inet4Address:             prefixStrings(tunConfig.Inet4Address),
		Inet6Address:             prefixStrings(tunConfig.Inet6Address),
		AutoRoute:                tunConfig.AutoRoute,
		Inet4RouteAddress:        inet4Routes,
		Inet6RouteAddress:        inet6Routes,
		Inet4RouteExcludeAddress: inet4Excluded,
		Inet6RouteExcludeAddress: inet6Excluded,
		DNSServerAddress:         dnsServerAddresses(tunConfig, dnsEnabled),
		IncludePackage:           uniqueSorted(append([]string{}, tunConfig.IncludePackage...)),
		ExcludePackage:           uniqueSorted(append([]string{}, tunConfig.ExcludePackage...)),
	}
}

func splitPrefixes(prefixes []netip.Prefix) ([]string, []string) {
	var inet4 []string
	var inet6 []string
	for _, prefix := range prefixes {
		if !prefix.IsValid() {
			continue
		}
		if prefix.Addr().Is4() {
			inet4 = append(inet4, prefix.String())
		} else {
			inet6 = append(inet6, prefix.String())
		}
	}
	return uniqueSorted(inet4), uniqueSorted(inet6)
}

func prefixStrings(prefixes []netip.Prefix) []string {
	result := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		if prefix.IsValid() {
			result = append(result, prefix.String())
		}
	}
	return uniqueSorted(result)
}

func dnsServerAddresses(tunConfig LC.Tun, enabled bool) []string {
	if !enabled {
		return nil
	}
	result := make([]string, 0, len(tunConfig.Inet4Address)+len(tunConfig.Inet6Address))
	addresses := append([]netip.Prefix{}, tunConfig.Inet4Address...)
	addresses = append(addresses, tunConfig.Inet6Address...)
	for _, prefix := range addresses {
		if !prefix.IsValid() {
			continue
		}
		address := prefix.Addr().Next()
		if address.IsValid() && prefix.Contains(address) {
			result = append(result, address.String())
		}
	}
	return uniqueSorted(result)
}

func uniqueSorted(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if value == "" || (len(result) != 0 && result[len(result)-1] == value) {
			continue
		}
		result = append(result, value)
	}
	return result
}
