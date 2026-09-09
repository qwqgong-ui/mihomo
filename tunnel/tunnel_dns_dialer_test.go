package tunnel

import (
	"errors"
	"testing"

	C "github.com/metacubex/mihomo/constant"
	"github.com/stretchr/testify/require"
)

type tunnelDNSNodeStub struct {
	name  string
	type_ C.AdapterType
}

func (n tunnelDNSNodeStub) Name() string        { return n.name }
func (n tunnelDNSNodeStub) Type() C.AdapterType { return n.type_ }

func TestTunnelDNSNodeUsableDistinguishesDirectFromOtherLocalAdapters(t *testing.T) {
	directErr := tunnelDNSNodeUsable(tunnelDNSNodeStub{name: "DIRECT", type_: C.Direct})
	require.ErrorIs(t, directErr, ErrTunnelDNSDirectNode)
	require.ErrorIs(t, directErr, ErrTunnelDNSUnsupported)

	for _, adapterType := range []C.AdapterType{C.Reject, C.RejectDrop, C.Compatible, C.Pass, C.PassRule, C.Rematch, C.Dns} {
		err := tunnelDNSNodeUsable(tunnelDNSNodeStub{name: adapterType.String(), type_: adapterType})
		require.ErrorIs(t, err, ErrTunnelDNSUnsupported)
		require.False(t, errors.Is(err, ErrTunnelDNSDirectNode), adapterType.String())
	}
}
