//go:build android

package androidplatform

import (
	"net/netip"

	"github.com/metacubex/mihomo/component/process"
)

func resolveProcess(network string, src, dst netip.AddrPort) (uint32, string, error) {
	if !src.IsValid() || !dst.IsValid() || !Enabled() {
		return 0, "", process.ErrNotFound
	}
	response, descriptor, err := exchangePlatformRequest(platformRequest{
		Operation:          "find_process",
		Network:            network,
		SourceAddress:      src.Addr().String(),
		SourcePort:         src.Port(),
		DestinationAddress: dst.Addr().String(),
		DestinationPort:    dst.Port(),
	}, false)
	closeDescriptor(descriptor)
	if err != nil || response.UID < 0 || response.PackageName == "" {
		return 0, "", process.ErrNotFound
	}
	return uint32(response.UID), response.PackageName, nil
}
