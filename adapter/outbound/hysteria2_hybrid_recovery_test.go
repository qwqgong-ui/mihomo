package outbound

import (
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	M "github.com/metacubex/sing/common/metadata"
)

// onlyFlow returns the single flow the harness has opened, so a test can put it
// in a state that would otherwise take a wall-clock wait to reach.
func onlyFlow(t *testing.T, h *hybridHarness) *hybridQUICFlow {
	t.Helper()
	h.conn.mu.Lock()
	defer h.conn.mu.Unlock()
	if len(h.conn.flows) != 1 {
		t.Fatalf("the connection holds %d flows, want 1", len(h.conn.flows))
	}
	for _, flow := range h.conn.flows {
		return flow
	}
	return nil
}

// settleOnRawPath drives a flow through registration and handover until the raw
// path has answered and the tunnel copy has stopped.
func settleOnRawPath(t *testing.T, h *hybridHarness, destination net.Addr) *hybridQUICFlow {
	t.Helper()
	if _, err := h.conn.WriteTo(hybridInitialPacket(), destination); err != nil {
		t.Fatal(err)
	}
	control := h.hy2.awaitSent(t, 1)[0]
	var id [16]byte
	copy(id[:], control.data[5:21])
	h.hy2.deliver(hybridAckWithTarget(id, netip.MustParseAddrPort("[2606:4700:4700::1111]:443")), hybridControlAddr{})

	if _, err := h.conn.WriteTo(hybrid1RTTPacket(), destination); err != nil {
		t.Fatal(err)
	}
	h.raw.awaitSent(t, 1)
	// A name flow cannot label a raw datagram until the acknowledgement names
	// the address the server resolved, and that arrives on its own goroutine.
	awaitResolved(t, h)
	h.raw.deliver([]byte("raw reply"), net.UDPAddrFromAddrPort(h.relay))
	if _, _, data := h.readWithin(t, 2*time.Second); string(data) != "raw reply" {
		t.Fatalf("raw reply = %q", data)
	}

	flow := onlyFlow(t, h)
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.conn.mu.Lock()
		phase := flow.phase
		h.conn.mu.Unlock()
		if phase == hybridPhaseRaw {
			return flow
		}
		if time.Now().After(deadline) {
			t.Fatalf("the flow settled in phase %d, want the raw path", phase)
		}
		time.Sleep(time.Millisecond)
	}
}

// A raw path that worked once is not a raw path that works now: the server
// reclaims an idle flow, a NAT mapping expires, a route changes. Nothing in the
// flow would notice -- it would keep sending into a path that no longer answers
// -- so a stretch of silence has to put it back on both paths.
func TestHybridClientLeavesASilentRawPath(t *testing.T) {
	h := newHybridHarness(t)
	destination := M.ParseSocksaddrHostPort("example.com", 443)
	flow := settleOnRawPath(t, h, destination)

	// While the raw path is answering, the tunnel carries nothing.
	tunnelBefore := len(h.hy2.sent())
	if _, err := h.conn.WriteTo(hybrid1RTTPacket(), destination); err != nil {
		t.Fatal(err)
	}
	h.raw.awaitSent(t, 2)
	if len(h.hy2.sent()) != tunnelBefore {
		t.Fatal("the tunnel carried a 1-RTT packet while the raw path was live")
	}

	// Age the last raw answer past the window the flow will tolerate.
	h.conn.mu.Lock()
	flow.lastRaw = time.Now().Add(-hybridRawSilence - time.Second)
	h.conn.mu.Unlock()

	// The next packet goes back on the tunnel, with a copy probing the raw
	// path, so nothing is lost while the flow works out whether it recovered.

	tunnelBefore = len(h.hy2.sent())
	rawBefore := len(h.raw.sent())
	if _, err := h.conn.WriteTo(hybrid1RTTPacket(), destination); err != nil {
		t.Fatal(err)
	}
	if len(h.hy2.sent()) <= tunnelBefore {
		t.Fatal("a flow whose raw path went quiet did not go back to the tunnel")
	}
	if len(h.raw.sent()) <= rawBefore {
		t.Fatal("a flow whose raw path went quiet stopped probing it altogether")
	}
	h.conn.mu.Lock()
	phase := flow.phase
	h.conn.mu.Unlock()
	if phase != hybridPhaseTunnel {
		t.Fatalf("phase = %d, want the tunnel", phase)
	}
	if len(h.fallback.sent()) != 0 {
		t.Fatal("a registered flow used the fallback path")
	}
}

// The tunnel is a separate path and may well be fine, so a raw socket that has
// stopped accepting writes costs the flow its raw path, not its connection.
func TestHybridClientSurvivesARawWriteFailure(t *testing.T) {
	h := newHybridHarness(t)
	destination := M.ParseSocksaddrHostPort("example.com", 443)
	flow := settleOnRawPath(t, h, destination)

	h.raw.failWrites(errors.New("network is unreachable"))
	tunnelBefore := len(h.hy2.sent())

	payload := hybrid1RTTPacket()
	n, err := h.conn.WriteTo(payload, destination)
	if err != nil {
		t.Fatalf("a raw write failure was reported to the caller: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("WriteTo = %d, want %d", n, len(payload))
	}
	sent := h.hy2.awaitSent(t, tunnelBefore+1)
	relayed := sent[len(sent)-1]
	if relayed.data[4] != hybridQUICRelay {
		t.Fatalf("the tunnel carried op %d, want a relay", relayed.data[4])
	}
	h.conn.mu.Lock()
	phase := flow.phase
	h.conn.mu.Unlock()
	if phase != hybridPhaseTunnel {
		t.Fatalf("phase = %d, want the tunnel", phase)
	}
}

// A PacketConn truncates an oversized datagram. Returning an error instead is
// read as fatal by the QUIC stack above, which takes the whole connection down
// over one packet that was merely too big for the caller's buffer.
func TestHybridClientTruncatesAShortReadBuffer(t *testing.T) {
	h := newHybridHarness(t)
	target := netip.MustParseAddrPort("[2606:4700:4700::1111]:443")
	payload := make([]byte, 100)
	for i := range payload {
		payload[i] = byte(i)
	}
	h.hy2.deliver(payload, net.UDPAddrFromAddrPort(target))

	buffer := make([]byte, 10)
	n, addr, err := h.conn.ReadFrom(buffer)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if n != len(buffer) {
		t.Fatalf("ReadFrom = %d bytes, want %d", n, len(buffer))
	}
	if addr.String() != target.String() {
		t.Fatalf("datagram came from %v, want %v", addr, target)
	}
	for i := range buffer {
		if buffer[i] != byte(i) {
			t.Fatalf("truncated datagram = %v", buffer)
		}
	}
}

// A name flow has no address of its own until the registration is acknowledged.
// Delivering a raw datagram before then would attribute it to nowhere, and the
// sender's mapping would not resolve it.
func TestHybridClientHoldsRawRepliesUntilTheNameResolves(t *testing.T) {
	h := newHybridHarness(t)
	destination := M.ParseSocksaddrHostPort("example.com", 443)

	if _, err := h.conn.WriteTo(hybridInitialPacket(), destination); err != nil {
		t.Fatal(err)
	}
	control := h.hy2.awaitSent(t, 1)[0]
	var id [16]byte
	copy(id[:], control.data[5:21])

	// Reordered ahead of the acknowledgement: there is no address to label it
	// with yet, so it is dropped rather than delivered from 0.0.0.0:0.
	h.raw.deliver([]byte("too early"), net.UDPAddrFromAddrPort(h.relay))
	time.Sleep(50 * time.Millisecond)

	target := netip.MustParseAddrPort("[2606:4700:4700::1111]:443")
	h.hy2.deliver(hybridAckWithTarget(id, target), hybridControlAddr{})
	awaitResolved(t, h)
	h.raw.deliver([]byte("in time"), net.UDPAddrFromAddrPort(h.relay))

	// The first datagram the caller sees is the one that arrived with an
	// address to label it with.
	_, addr, data := h.readWithin(t, 2*time.Second)
	if string(data) != "in time" {
		t.Fatalf("first delivered datagram = %q, want the one that arrived after the ack", data)
	}
	if addr.String() != target.String() {
		t.Fatalf("datagram came from %v, want %v", addr, target)
	}
}

// onlyFlowLocked is onlyFlow for a caller that already holds the connection's
// lock.
func onlyFlowLocked(h *hybridHarness) *hybridQUICFlow {
	for _, flow := range h.conn.flows {
		return flow
	}
	return nil
}

// awaitResolved waits for the registration acknowledgement to be applied.
func awaitResolved(t *testing.T, h *hybridHarness) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.conn.mu.Lock()
		flow := onlyFlowLocked(h)
		resolved := flow != nil && flow.resolved.IsValid()
		h.conn.mu.Unlock()
		if resolved {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the registration was never acknowledged with a resolved address")
		}
		time.Sleep(time.Millisecond)
	}
}
