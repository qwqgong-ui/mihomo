package process

import (
	"errors"
	"net/netip"

	C "github.com/metacubex/mihomo/constant"
)

var (
	ErrInvalidNetwork     = errors.New("invalid network")
	ErrPlatformNotSupport = errors.New("not support on this platform")
	ErrNotFound           = errors.New("process not found")
)

const (
	TCP = "tcp"
	UDP = "udp"
)

func FindProcessName(network string, srcIP netip.Addr, srcPort int) (uint32, string, error) {
	return findProcessName(network, srcIP, srcPort)
}

// ProcessNameResolver resolves a socket owner outside the mihomo process.
// Android VpnService supplies this because ordinary Android applications cannot
// depend on /proc/net or INET_DIAG access.
type ProcessNameResolver func(network string, src, dst netip.AddrPort) (uint32, string, error)

var DefaultProcessNameResolver ProcessNameResolver

// FindProcessNameByAddr finds the process owning a socket identified by its
// local and remote endpoints. Platforms without an endpoint-aware lookup fall
// back to the source-only implementation.
func FindProcessNameByAddr(network string, src, dst netip.AddrPort) (uint32, string, error) {
	if resolver := DefaultProcessNameResolver; resolver != nil {
		if uid, path, err := resolver(network, src, dst); err == nil {
			return uid, path, nil
		}
	}
	return findProcessNameByAddr(network, src, dst)
}

// PackageNameResolver
// never change type traits because it's used in CMFA
type PackageNameResolver func(metadata *C.Metadata) (string, error)

// DefaultPackageNameResolver
// never change type traits because it's used in CMFA
var DefaultPackageNameResolver PackageNameResolver

func FindPackageName(metadata *C.Metadata) (string, error) {
	if resolver := DefaultPackageNameResolver; resolver != nil {
		return resolver(metadata)
	}
	return "", ErrPlatformNotSupport
}
