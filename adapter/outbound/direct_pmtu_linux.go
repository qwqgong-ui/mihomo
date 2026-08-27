//go:build linux || android

package outbound

import (
	"context"
	"net"
	"net/netip"

	"github.com/metacubex/mihomo/component/dialer"

	"github.com/metacubex/sing/common"
	M "github.com/metacubex/sing/common/metadata"
	"golang.org/x/sys/unix"
)

const pathMTUSupported = true

// queryPathMTU reads the MTU the kernel will allow towards a destination. The
// value is only readable from a connected socket, and connecting a UDP socket
// sends nothing, so a throwaway one - listened with the same options, hence
// bound the same way and routed the same way - answers the question without
// disturbing the socket carrying the flow.
func queryPathMTU(ctx context.Context, destination netip.AddrPort, options []dialer.Option) uint32 {
	network := "udp4"
	if !destination.Addr().Unmap().Is4() {
		network = "udp6"
	}
	packetConn, err := dialer.ListenPacket(ctx, network, "", destination, options...)
	if err != nil {
		return 0
	}
	defer packetConn.Close()
	udpConn, isUDPConn := common.Cast[*net.UDPConn](packetConn)
	if !isUDPConn {
		return 0
	}
	rawConn, err := udpConn.SyscallConn()
	if err != nil {
		return 0
	}
	var (
		mtu     int
		sockErr error
	)
	controlErr := rawConn.Control(func(fd uintptr) {
		sockErr = unix.Connect(int(fd), M.AddrPortToSockaddr(destination))
		if sockErr != nil {
			return
		}
		if destination.Addr().Unmap().Is4() {
			mtu, sockErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU)
		} else {
			mtu, sockErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_MTU)
		}
	})
	if controlErr != nil || sockErr != nil || mtu <= 0 {
		return 0
	}
	return uint32(mtu)
}
