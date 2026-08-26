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

// ProcessMatcher reports whether an executable path can match any configured
// process rule. It is used as a hint only: implementations must preserve the
// unfiltered lookup when no matcher is supplied.
type ProcessMatcher interface {
	MatchProcess(path string) bool
	MatchProcessName(name string) bool
}

// RuleProcessMatcher is implemented by rules and rule containers that can
// expose process candidates without evaluating socket ownership.
type RuleProcessMatcher interface {
	ProcessMatcher
	HasProcessRule() bool
}

func FindProcessName(network string, srcIP netip.Addr, srcPort int) (uint32, string, error) {
	return findProcessName(network, srcIP, srcPort)
}

// FindProcessNameByAddr finds the process owning a socket identified by its
// local and remote endpoints. Platforms without an endpoint-aware lookup fall
// back to the source-only implementation.
func FindProcessNameByAddr(network string, src, dst netip.AddrPort) (uint32, string, error) {
	return findProcessNameByAddr(network, src, dst, nil)
}

// FindProcessNameByAddrWithMatcher limits expensive process descriptor scans
// to executable paths that can match the active process rules.
func FindProcessNameByAddrWithMatcher(network string, src, dst netip.AddrPort, matcher ProcessMatcher) (uint32, string, error) {
	return findProcessNameByAddr(network, src, dst, matcher)
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

func findPackageNameByUID(uid uint32) (string, error) {
	return FindPackageName(&C.Metadata{Uid: uid})
}

func matchPackageNameByUID(uid uint32, matcher ProcessMatcher) (matched, resolved bool) {
	pkg, err := findPackageNameByUID(uid)
	if err != nil {
		return false, false
	}
	return matcher.MatchProcessName(pkg), true
}
