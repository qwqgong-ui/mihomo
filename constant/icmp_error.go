package constant

import "strconv"

// ICMPError is a condition worth reporting back to the source of a UDP flow as
// an ICMP error. A datagram that cannot be forwarded otherwise leaves no trace
// for the sender: it is talking to a tun device that accepted the packet, so it
// keeps sending the same size, or waits out a timeout an unreachable would have
// ended at once.
type ICMPError int

const (
	ICMPErrorPacketTooBig ICMPError = iota
	ICMPErrorPortUnreachable
	ICMPErrorHostUnreachable
	ICMPErrorNetworkUnreachable
)

func (e ICMPError) String() string {
	switch e {
	case ICMPErrorPacketTooBig:
		return "packet too big"
	case ICMPErrorPortUnreachable:
		return "port unreachable"
	case ICMPErrorHostUnreachable:
		return "host unreachable"
	case ICMPErrorNetworkUnreachable:
		return "network unreachable"
	}
	return "icmp error " + strconv.Itoa(int(e))
}

// UDPPacketICMPError is implemented by inbound UDP packets whose source can be
// told why the packet went no further. mtu is only read for
// ICMPErrorPacketTooBig, and is the largest datagram the source may send,
// measured the way the source measures it.
type UDPPacketICMPError interface {
	ReportICMPError(icmpError ICMPError, mtu uint32) error
}

// PacketTooBigError reports a write that failed because the datagram exceeded
// the path MTU, along with the MTU the path does allow.
type PacketTooBigError struct {
	MTU uint32
	Err error
}

func (e *PacketTooBigError) Error() string {
	return "packet too big, path mtu " + strconv.Itoa(int(e.MTU)) + ": " + e.Err.Error()
}

func (e *PacketTooBigError) Unwrap() error {
	return e.Err
}
