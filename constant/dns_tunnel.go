package constant

// TunnelDNS is the reserved destination a client dials when it wants the proxy
// server's own resolver to answer a question, rather than a resolver of its
// own. It is deliberately a name in the `.invalid` TLD (RFC 6761): it has no
// address anywhere, so it can never resolve and can never be routed to a real
// host. Both ends recognise it internally and it never reaches the wire as a
// lookup.
//
// It is only a destination, so it needs no protocol of its own -- what travels
// inside is an ordinary DNS message, and any proxy protocol can carry it.
//
// The node is never chosen by this name. It is chosen from the real domain
// being asked about, exactly as the traffic to that domain would be, and only
// then is this destination dialed through the node that came out. Matching
// rules against this name instead would pick a second, unrelated node and
// answer with a record that no connection is ever built on.
const (
	TunnelDNSHost = "server-dns.invalid"
	TunnelDNSPort = "53"
)

// TunnelDNSAddress is TunnelDNSHost joined with its port.
const TunnelDNSAddress = TunnelDNSHost + ":" + TunnelDNSPort

// IsTunnelDNSHost reports whether host addresses the reserved destination.
// Callers use it to skip local name resolution: the name must be handed to the
// proxy untouched.
func IsTunnelDNSHost(host string) bool {
	return host == TunnelDNSHost
}

// CanServeTunnelDNS reports whether an adapter of this type could have a proxy
// server behind it to ask. Traffic that never leaves this machine, or that is
// answered locally, has no server whose resolver could differ from ours.
func (at AdapterType) CanServeTunnelDNS() bool {
	switch at {
	case Direct, Reject, RejectDrop, Compatible, Pass, PassRule, Rematch, Dns:
		return false
	default:
		return true
	}
}
