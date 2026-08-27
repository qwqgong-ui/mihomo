//go:build linux || android

package sockopt

import (
	"golang.org/x/sys/unix"
)

func dontFragmentControl(fd uintptr) error {
	errIPv4 := unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO)
	// IPv6 has no fragmentation in transit at all: a source that wants a
	// datagram delivered whole is entitled to a Packet Too Big instead of a
	// silently fragmented copy.
	errIPv6 := unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_DONTFRAG, 1)
	if errIPv4 != nil && errIPv6 != nil {
		return errIPv4
	}
	return nil
}
