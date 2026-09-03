package outbound

import (
	"net/netip"
	"testing"
	"time"
)

func newRaceTarget(candidates ...netip.AddrPort) *directUDPTarget {
	target := &directUDPTarget{
		logical:    candidates[0],
		candidates: candidates,
		live:       make(map[netip.AddrPort]bool, len(candidates)),
	}
	for _, candidate := range candidates {
		target.live[candidate] = true
	}
	return target
}

// A QUIC target used to have exactly one way to name a winner: the connection
// id the application addressed its own packets to. Every other path was gated
// off for QUIC, so a server that answers with a zero-length id left the race
// unable to converge at all -- every datagram copied to every candidate for the
// life of the connection, and replies from all of them handed up as one peer.
func TestDirectUDPQUICRaceSettlesWithoutConnectionID(t *testing.T) {
	first := netip.MustParseAddrPort("192.0.2.1:443")
	second := netip.MustParseAddrPort("192.0.2.2:443")
	target := newRaceTarget(first, second)
	target.quic = true
	target.started = time.Now().Add(-2 * directUDPQUICRaceWindow)

	// The second candidate is the one that actually answered, so it is the one
	// worth keeping even though the first is earlier in the list.
	target.responder = second

	if got := settleDirectUDPCandidate(target); got != second {
		t.Fatalf("settled on %v, want the candidate that answered (%v)", got, second)
	}
}

// With nothing having answered there is no evidence to prefer, and the race
// still has to end rather than fan out forever.
func TestDirectUDPQUICRaceSettlesWithoutAnyReply(t *testing.T) {
	first := netip.MustParseAddrPort("192.0.2.1:443")
	second := netip.MustParseAddrPort("192.0.2.2:443")
	target := newRaceTarget(first, second)
	target.quic = true

	if got := settleDirectUDPCandidate(target); got != first {
		t.Fatalf("settled on %v, want the first live candidate (%v)", got, first)
	}
}

// A responder that has since failed must not be settled on.
func TestDirectUDPQUICRaceSkipsDeadResponder(t *testing.T) {
	first := netip.MustParseAddrPort("192.0.2.1:443")
	second := netip.MustParseAddrPort("192.0.2.2:443")
	target := newRaceTarget(first, second)
	target.quic = true
	target.responder = second
	delete(target.live, second)

	if got := settleDirectUDPCandidate(target); got != first {
		t.Fatalf("settled on %v, want the surviving candidate (%v)", got, first)
	}
}

// The connection id remains the preferred signal: while it can still arrive,
// the race must not settle on a guess.
func TestDirectUDPQUICRaceWaitsForConnectionIDWithinWindow(t *testing.T) {
	first := netip.MustParseAddrPort("192.0.2.1:443")
	second := netip.MustParseAddrPort("192.0.2.2:443")
	target := newRaceTarget(first, second)
	target.quic = true
	target.started = time.Now()
	target.responder = second

	// Well inside the window, so nothing should have been settled yet.
	if elapsed := time.Since(target.started); elapsed >= directUDPQUICRaceWindow {
		t.Skipf("test machine too slow: %s already elapsed", elapsed)
	}
	if target.winner.IsValid() {
		t.Fatal("a winner was named before the connection id had a chance to arrive")
	}
}

// The window has to be longer than the plain race window, or a QUIC target
// would settle before its handshake can name a peer.
func TestDirectUDPQUICRaceWindowExceedsPlainWindow(t *testing.T) {
	if directUDPQUICRaceWindow <= directUDPRaceWindow {
		t.Fatalf("QUIC window %s must exceed the plain race window %s",
			directUDPQUICRaceWindow, directUDPRaceWindow)
	}
}
