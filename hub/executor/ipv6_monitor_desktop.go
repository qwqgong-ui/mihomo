//go:build (linux && !android) || windows || darwin

package executor

import (
	"github.com/metacubex/mihomo/log"
	tun "github.com/metacubex/sing-tun"
)

// newRuntimeIPv6NetworkUpdateMonitor wraps sing-tun's OS network-change
// monitor (netlink on Linux, route messages on Windows/macOS) so
// hub/executor can re-detect IPv6 availability on interface/route changes
// instead of polling.
func newRuntimeIPv6NetworkUpdateMonitor(callback func()) (runtimeIPv6NetworkMonitor, error) {
	monitor, err := tun.NewNetworkUpdateMonitor(log.SingLogger)
	if err != nil {
		return nil, err
	}
	monitor.RegisterCallback(callback)
	return monitor, nil
}
