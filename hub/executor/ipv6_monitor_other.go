//go:build android || (!linux && !windows && !darwin)

package executor

// newRuntimeIPv6NetworkUpdateMonitor has no implementation on Android or
// other unsupported platforms: on Android, netlink access is restricted on
// modern SDKs and network-switch handling is deliberately left to the host
// application (which already knows when its own VPN/network state changes);
// elsewhere sing-tun has no monitor implementation to wrap. A nil monitor
// with a nil error means the runtime IPv6 controller simply never starts a
// monitor there - config-time detection (config.SystemIPv6Available, run at
// parse time and on every ApplyConfig) still applies.
func newRuntimeIPv6NetworkUpdateMonitor(func()) (runtimeIPv6NetworkMonitor, error) {
	return nil, nil
}
