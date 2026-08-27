//go:build !linux && !android

package outbound

import (
	"context"
	"net/netip"

	"github.com/metacubex/mihomo/component/dialer"
)

const pathMTUSupported = false

func queryPathMTU(ctx context.Context, destination netip.AddrPort, options []dialer.Option) uint32 {
	return 0
}
