package tunnel

import (
	"context"
	"net"
	"testing"

	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/component/tunneldns"
	C "github.com/metacubex/mihomo/constant"
	R "github.com/metacubex/mihomo/rules"
)

// recordingProxy is a node that accepts one dial and remembers what it was
// asked for. Only the methods this path reaches are implemented; the embedded
// interface makes any other call a loud failure rather than a silent pass.
type recordingProxy struct {
	C.Proxy
	name   string
	kind   C.AdapterType
	dialed *C.Metadata
}

func (p *recordingProxy) Name() string                     { return p.name }
func (p *recordingProxy) Type() C.AdapterType              { return p.kind }
func (p *recordingProxy) Unwrap(*C.Metadata, bool) C.Proxy { return nil }
func (p *recordingProxy) SupportUDP() bool                 { return true }
func (p *recordingProxy) DialContext(_ context.Context, metadata *C.Metadata) (C.Conn, error) {
	p.dialed = metadata
	client, server := net.Pipe()
	go func() { _, _ = server.Read(make([]byte, 1)); _ = server.Close() }()
	return &recordingConn{ExtendedConn: N.NewExtendedConn(client)}, nil
}

type recordingConn struct {
	N.ExtendedConn
}

func (c *recordingConn) Chains() C.Chain                 { return C.Chain{"recording"} }
func (c *recordingConn) AppendToChains(a C.ProxyAdapter) { _ = a }
func (c *recordingConn) RemoteDestination() string       { return "" }
func (c *recordingConn) ProviderChains() C.Chain         { return nil }

// installNode points every rule at one node for the duration of a test.
func installNode(t *testing.T, node C.Proxy, ruleType, payload string) {
	t.Helper()
	previousProxies, previousRules := Proxies(), Rules()
	parsed, err := R.ParseRule(ruleType, payload, node.Name(), nil, nil)
	if err != nil {
		t.Fatalf("rule: %v", err)
	}
	UpdateProxies(map[string]C.Proxy{node.Name(): node}, nil)
	UpdateRules([]C.Rule{parsed}, nil, nil)
	t.Cleanup(func() {
		UpdateProxies(previousProxies, nil)
		UpdateRules(previousRules, nil, nil)
		tunneldns.Reset()
	})
}

// The node is chosen from the domain in the question, and the connection made
// through it goes to the reserved destination. Getting these two the wrong way
// round is the whole failure mode this design exists to avoid: rules matched
// against the reserved name would pick some unrelated node, and its record
// would describe a connection nobody makes.
func TestDialTunnelDNSSelectsByDomainAndDialsTheReservedName(t *testing.T) {
	node := &recordingProxy{name: "JP-01", kind: C.Shadowsocks}
	installNode(t, node, "DOMAIN", "example.com")

	conn, selected, err := DialTunnelDNS(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if selected != "JP-01" {
		t.Fatalf("node = %q, want the one example.com routes to", selected)
	}
	if node.dialed == nil {
		t.Fatal("the node was never dialed")
	}
	if !C.IsTunnelDNSHost(node.dialed.Host) {
		t.Fatalf("dialed %q, want the reserved destination", node.dialed.Host)
	}
	if node.dialed.DstPort != 53 {
		t.Fatalf("dialed port %d, want 53", node.dialed.DstPort)
	}
}

// A rule that does not cover the queried domain must not be able to catch this
// query by covering the reserved name instead -- there is no such rule to
// write, and selection never sees that name.
func TestDialTunnelDNSIgnoresRulesForTheReservedName(t *testing.T) {
	node := &recordingProxy{name: "JP-01", kind: C.Shadowsocks}
	installNode(t, node, "DOMAIN", C.TunnelDNSHost)

	if _, _, err := DialTunnelDNS(context.Background(), "example.com"); err == nil {
		t.Fatal("a rule written for the reserved name must not select a node for another domain")
	}
	if node.dialed != nil {
		t.Fatal("no node should have been dialed")
	}
}

// A node already found not to answer is not dialed again.
func TestDialTunnelDNSSkipsANodeThatDidNotAnswer(t *testing.T) {
	node := &recordingProxy{name: "JP-02", kind: C.Shadowsocks}
	installNode(t, node, "DOMAIN", "example.com")
	tunneldns.MarkUnsupported("JP-02")

	if _, _, err := DialTunnelDNS(context.Background(), "example.com"); err == nil {
		t.Fatal("a silenced node must not be dialed")
	}
	if node.dialed != nil {
		t.Fatal("the node was dialed despite its verdict")
	}
}
