//go:build windows

package dns

import (
	"net"
	"net/netip"
	"os"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/exp/slices"
	"golang.org/x/sys/windows"
)

func dnsReadConfig() (servers []systemNameServer, err error) {
	aas, err := adapterAddresses()
	if err != nil {
		return nil, err
	}

	aa := defaultAdapter(aas, defaultPhysicalInterface())
	if aa == nil {
		return nil, nil
	}
	interfaceName := adapterInterfaceName(aa)
	for dns := aa.FirstDnsServerAddress; dns != nil; dns = dns.Next {
		ip, ok := socketAddressIP(dns.Address)
		if !ok || !usableSystemDNS(ip) {
			continue
		}
		server := systemNameServer{address: ip.String(), interfaceName: interfaceName}
		if !slices.Contains(servers, server) {
			servers = append(servers, server)
		}
	}
	return servers, nil
}

// defaultAdapter selects exactly one physical default outlet. The TUN default
// interface monitor wins when available. The metric fallback deliberately only
// considers live adapters with a gateway, preserving the old TUN exclusion.
func defaultAdapter(aas []*windows.IpAdapterAddresses, preferredName string) *windows.IpAdapterAddresses {
	var best *windows.IpAdapterAddresses
	bestMetric := ^uint32(0)
	for _, aa := range aas {
		if aa.OperStatus != windows.IfOperStatusUp || aa.FirstGatewayAddress == nil {
			continue
		}
		name := adapterInterfaceName(aa)
		if preferredName != "" && name == preferredName {
			return aa
		}
		metric := aa.Ipv4Metric
		if aa.Ipv6Metric < metric {
			metric = aa.Ipv6Metric
		}
		if best == nil || metric < bestMetric {
			best = aa
			bestMetric = metric
		}
	}
	return best
}

func adapterInterfaceName(aa *windows.IpAdapterAddresses) string {
	if aa.IfIndex != 0 {
		if iface, err := net.InterfaceByIndex(int(aa.IfIndex)); err == nil {
			return iface.Name
		}
	}
	if aa.Ipv6IfIndex != 0 {
		if iface, err := net.InterfaceByIndex(int(aa.Ipv6IfIndex)); err == nil {
			return iface.Name
		}
	}
	return windows.UTF16PtrToString(aa.FriendlyName)
}

func socketAddressIP(address windows.SocketAddress) (netip.Addr, bool) {
	sa, err := address.Sockaddr.Sockaddr()
	if err != nil {
		return netip.Addr{}, false
	}
	switch sa := sa.(type) {
	case *syscall.SockaddrInet4:
		return netip.AddrFrom4(sa.Addr), true
	case *syscall.SockaddrInet6:
		ip := netip.AddrFrom16(sa.Addr)
		if sa.ZoneId != 0 {
			ip = ip.WithZone(strconv.FormatUint(uint64(sa.ZoneId), 10))
		}
		return ip, true
	default:
		return netip.Addr{}, false
	}
}

func usableSystemDNS(ip netip.Addr) bool {
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return false
	}
	// Windows still populates deprecated site-local fec0::/10 placeholders on
	// miscellaneous adapters. They are not usable resolvers.
	return !(ip.Is6() && ip.As16()[0] == 0xfe && ip.As16()[1]&0xc0 == 0xc0)
}

// adapterAddresses returns a list of IP adapter and address structures.
func adapterAddresses() ([]*windows.IpAdapterAddresses, error) {
	var b []byte
	l := uint32(15000)
	for {
		b = make([]byte, l)
		const flags = windows.GAA_FLAG_INCLUDE_PREFIX | windows.GAA_FLAG_INCLUDE_GATEWAYS
		err := windows.GetAdaptersAddresses(syscall.AF_UNSPEC, flags, 0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&b[0])), &l)
		if err == nil {
			if l == 0 {
				return nil, nil
			}
			break
		}
		if err.(syscall.Errno) != syscall.ERROR_BUFFER_OVERFLOW {
			return nil, os.NewSyscallError("getadaptersaddresses", err)
		}
		if l <= uint32(len(b)) {
			return nil, os.NewSyscallError("getadaptersaddresses", err)
		}
	}
	var aas []*windows.IpAdapterAddresses
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&b[0])); aa != nil; aa = aa.Next {
		aas = append(aas, aa)
	}
	return aas, nil
}
