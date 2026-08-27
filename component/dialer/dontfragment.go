package dialer

import (
	"context"
	"net"
	"syscall"

	"github.com/metacubex/mihomo/common/sockopt"
)

func dontFragmentToListenConfig(lc *net.ListenConfig) {
	addControlToListenConfig(lc, func(ctx context.Context, network, address string, c syscall.RawConn) error {
		return sockopt.RawConnDontFragment(c)
	})
}
