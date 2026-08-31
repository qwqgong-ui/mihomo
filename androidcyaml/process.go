package androidcyaml

import (
	"net/netip"

	"github.com/metacubex/mihomo/component/process"
)

// FindProcessMode is the strict / always / off selector from the AndroidCyaml
// override panel.
type FindProcessMode = process.FindProcessMode

// Errors a ProcessResolver returns. A resolver failure is authoritative: mihomo
// does not fall through to another mechanism afterwards.
var (
	ErrProcessNotFound   = process.ErrNotFound
	ErrInvalidNetwork    = process.ErrInvalidNetwork
	ErrProcessNotSupport = process.ErrPlatformNotSupport
)

// ParseFindProcessMode maps a wire value onto a mode.
func ParseFindProcessMode(value string) (FindProcessMode, error) {
	var mode FindProcessMode
	if err := mode.Set(value); err != nil {
		return mode, err
	}
	return mode, nil
}

// ProcessResolver answers which application owns a socket, identified by both
// endpoints.
//
// On Android the only permitted mechanism is
// ConnectivityManager.getConnectionOwnerUid, a Binder call into system_server.
// The Linux procfs and inet_diag paths mihomo uses elsewhere are blocked by
// SELinux, so a failure here is final rather than a reason to try again another
// way. It returns the owning UID and the package name together, since the
// platform lookup already has both.
type ProcessResolver func(network string, src, dst netip.AddrPort) (uint32, string, error)

// SetProcessResolver installs the platform lookup. Passing nil restores
// mihomo's own behavior.
func SetProcessResolver(resolver ProcessResolver) {
	if resolver == nil {
		process.SetEndpointResolver(nil)
		return
	}
	process.SetEndpointResolver(process.EndpointResolver(resolver))
}

// ResetProcessResolver removes the platform lookup.
func ResetProcessResolver() {
	process.SetEndpointResolver(nil)
}
