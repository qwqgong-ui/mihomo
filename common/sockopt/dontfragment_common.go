package sockopt

import (
	"syscall"
)

// RawConnDontFragment asks the kernel not to fragment datagrams sent on this
// socket, and to report the path MTU instead.
//
// Both the IPv4 and the IPv6 option are set: a socket listening on the
// unspecified address is an AF_INET6 one that carries IPv4 traffic too, and
// each family is governed by its own option. Failing one is not failing the
// call - an AF_INET socket has no IPv6 option to set - but failing both is.
func RawConnDontFragment(rc syscall.RawConn) (err error) {
	var innerErr error
	err = rc.Control(func(fd uintptr) {
		innerErr = dontFragmentControl(fd)
	})
	if innerErr != nil {
		err = innerErr
	}
	return
}
