//go:build !with_gvisor

package outbound

import (
	"errors"
	"net/netip"
)

func newGVisorIPStack([]netip.Prefix, uint32) (ipStack, error) {
	return nil, errors.New("gVisor IP stack requires the with_gvisor build tag")
}
