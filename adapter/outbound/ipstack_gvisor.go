//go:build with_gvisor

package outbound

import (
	"net/netip"

	wireguard "github.com/metacubex/sing-wireguard"
)

func newGVisorIPStack(localAddresses []netip.Prefix, mtu uint32) (ipStack, error) {
	return wireguard.NewStackDevice(localAddresses, mtu)
}

var _ ipStack = (wireguard.Device)(nil)
