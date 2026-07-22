//go:build !linux

package process

import "net/netip"

func findProcessNameByAddr(network string, src, _ netip.AddrPort) (uint32, string, error) {
	if !src.IsValid() {
		return 0, "", ErrNotFound
	}
	return findProcessName(network, src.Addr(), int(src.Port()))
}
