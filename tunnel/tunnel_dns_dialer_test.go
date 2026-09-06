package tunnel

import (
	"errors"
	"testing"

	"github.com/metacubex/mihomo/component/tunneldns"
	C "github.com/metacubex/mihomo/constant"
)

// The node is selected from the domain the client asked about, on the port its
// traffic would use -- never from the reserved name. The reserved name has no
// address anywhere and belongs to no rule, so matching on it would pick some
// unrelated node and answer with a record no connection is built on.
func TestTunnelDNSMatchTargetIsTheQueriedDomain(t *testing.T) {
	match, err := tunnelDNSMatchTarget("example.com")
	if err != nil {
		t.Fatalf("match target: %v", err)
	}
	if match.Host != "example.com" {
		t.Fatalf("host = %q, want the queried domain", match.Host)
	}
	if C.IsTunnelDNSHost(match.Host) {
		t.Fatal("rules must never be matched against the reserved name")
	}
	if match.DstPort != 443 {
		t.Fatalf("port = %d, want the port the domain's own traffic uses", match.DstPort)
	}
	if match.NetWork != C.TCP {
		t.Fatalf("network = %v, want TCP", match.NetWork)
	}
	if match.DstIP.IsValid() {
		t.Fatal("selection must not carry a resolved address of its own")
	}

	if _, err := tunnelDNSMatchTarget(""); err == nil {
		t.Fatal("there is no node to select without a domain")
	}
}

type fakeNode struct {
	name string
	kind C.AdapterType
}

func (n fakeNode) Name() string        { return n.name }
func (n fakeNode) Type() C.AdapterType { return n.kind }

// Asking a local outbound for "the server's view" is meaningless: there is no
// server behind it. And a node already found not to serve the reserved
// destination is not asked again, or the fallback becomes a per-query tax.
func TestTunnelDNSNodeUsable(t *testing.T) {
	t.Cleanup(tunneldns.Reset)

	if err := tunnelDNSNodeUsable(fakeNode{name: "JP-01", kind: C.Shadowsocks}); err != nil {
		t.Fatalf("a proxy node must be usable: %v", err)
	}

	for _, kind := range []C.AdapterType{C.Direct, C.Reject, C.RejectDrop, C.Compatible, C.Pass, C.Dns} {
		err := tunnelDNSNodeUsable(fakeNode{name: kind.String(), kind: kind})
		if !errors.Is(err, ErrTunnelDNSUnsupported) {
			t.Fatalf("%s has no server to ask, got %v", kind, err)
		}
	}

	tunneldns.MarkUnsupported("JP-02")
	err := tunnelDNSNodeUsable(fakeNode{name: "JP-02", kind: C.Shadowsocks})
	if !errors.Is(err, ErrTunnelDNSUnsupported) {
		t.Fatalf("a node that did not answer must not be asked again, got %v", err)
	}
	if err := tunnelDNSNodeUsable(fakeNode{name: "JP-01", kind: C.Shadowsocks}); err != nil {
		t.Fatalf("one node's verdict must not apply to another: %v", err)
	}
}
