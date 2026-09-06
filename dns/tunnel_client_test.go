package dns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/fakeip"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/component/tunneldns"
	C "github.com/metacubex/mihomo/constant"
	icontext "github.com/metacubex/mihomo/context"

	D "github.com/miekg/dns"
)

// swapTunnelDialer installs a stub node dialer for one test.
//
// The stub keeps the one thing the real dialer does that matters here: it
// refuses a node already found not to answer, because that check can only live
// where node selection does. Everything else -- rule matching, the proxy
// itself -- is what is being stood in for.
func swapTunnelDialer(t *testing.T, stub func(context.Context, string) (net.Conn, string, error)) {
	t.Helper()
	previous := dialTunnelDNS
	dialTunnelDNS = func(ctx context.Context, domain string) (net.Conn, string, error) {
		conn, node, err := stub(ctx, domain)
		if err == nil && !tunneldns.Supported(node) {
			if conn != nil {
				_ = conn.Close()
			}
			return nil, node, errors.New("the selected node does not serve tunnel DNS")
		}
		return conn, node, err
	}
	t.Cleanup(func() {
		dialTunnelDNS = previous
		tunneldns.Reset()
	})
}

// serveOneQuery answers a single query on one end of a pipe, the way the proxy
// server does, and reports the question it was asked.
func serveOneQuery(t *testing.T, reply func(*D.Msg) *D.Msg) (net.Conn, chan D.Question) {
	t.Helper()
	client, server := net.Pipe()
	asked := make(chan D.Question, 1)
	go func() {
		defer server.Close()
		conn := &D.Conn{Conn: server}
		request, err := conn.ReadMsg()
		if err != nil {
			return
		}
		if len(request.Question) == 1 {
			asked <- request.Question[0]
		}
		_ = conn.WriteMsg(reply(request))
	}()
	return client, asked
}

func httpsQuery(name string) *D.Msg {
	m := new(D.Msg)
	m.SetQuestion(D.Fqdn(name), D.TypeHTTPS)
	return m
}

// The node must be picked from the domain in the question, never from the
// reserved name: the answer only means anything if it comes from the server
// the connection to that domain is built through.
func TestTunnelClientSelectsTheNodeByTheQueriedDomain(t *testing.T) {
	var selectedFor string
	swapTunnelDialer(t, func(_ context.Context, domain string) (net.Conn, string, error) {
		selectedFor = domain
		conn, _ := serveOneQuery(t, func(request *D.Msg) *D.Msg {
			response := new(D.Msg)
			response.SetReply(request)
			return response
		})
		return conn, "JP-01", nil
	})

	if _, err := newTunnelClient().ExchangeContext(context.Background(), httpsQuery("example.com")); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if selectedFor != "example.com" {
		t.Fatalf("node selected for %q, want the queried domain", selectedFor)
	}
	if !tunneldns.Supported("JP-01") {
		t.Fatal("a node that answered must stay in use")
	}
}

// What travels inside the tunnel is an ordinary DNS message -- the same
// question, unchanged -- and the reply is used as given.
func TestTunnelClientSendsAnOrdinaryQuery(t *testing.T) {
	target, err := D.NewRR(`example.com. 60 IN HTTPS 1 . alpn="h3"`)
	if err != nil {
		t.Fatalf("rr: %v", err)
	}

	var asked chan D.Question
	swapTunnelDialer(t, func(context.Context, string) (net.Conn, string, error) {
		conn, questions := serveOneQuery(t, func(request *D.Msg) *D.Msg {
			response := new(D.Msg)
			response.SetReply(request)
			response.Answer = []D.RR{target}
			return response
		})
		asked = questions
		return conn, "JP-01", nil
	})

	msg, err := newTunnelClient().ExchangeContext(context.Background(), httpsQuery("example.com"))
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	select {
	case question := <-asked:
		if question.Name != "example.com." || question.Qtype != D.TypeHTTPS || question.Qclass != D.ClassINET {
			t.Fatalf("the server saw %v, want the original question", question)
		}
	default:
		t.Fatal("the server was never asked")
	}
	if len(msg.Answer) != 1 || msg.Answer[0].String() != target.String() {
		t.Fatal("the server's record must be used as given")
	}
}

// A server that cannot be reached, and one that refuses, are both discovered
// once and then left alone -- otherwise the fallback becomes a tax on every
// query.
func TestTunnelClientRemembersANodeThatCannotAnswer(t *testing.T) {
	for name, reply := range map[string]func(*D.Msg) *D.Msg{
		"refused":         func(r *D.Msg) *D.Msg { m := new(D.Msg); m.SetRcode(r, D.RcodeRefused); return m },
		"not implemented": func(r *D.Msg) *D.Msg { m := new(D.Msg); m.SetRcode(r, D.RcodeNotImplemented); return m },
	} {
		t.Run(name, func(t *testing.T) {
			attempts := 0
			swapTunnelDialer(t, func(context.Context, string) (net.Conn, string, error) {
				if !tunneldns.Supported("JP-02") {
					return nil, "JP-02", errors.New("not asked")
				}
				attempts++
				conn, _ := serveOneQuery(t, reply)
				return conn, "JP-02", nil
			})

			client := newTunnelClient()
			if _, err := client.ExchangeContext(context.Background(), httpsQuery("example.com")); err == nil {
				t.Fatal("a refusal must be reported so the caller falls back")
			}
			if tunneldns.Supported("JP-02") {
				t.Fatal("a node that refused must not be asked again")
			}
			if _, err := client.ExchangeContext(context.Background(), httpsQuery("other.example")); err == nil {
				t.Fatal("the second query must fall back too")
			}
			if attempts != 1 {
				t.Fatalf("connections = %d, want 1: the verdict must spare the second query", attempts)
			}
		})
	}
}

func TestTunnelClientRemembersANodeThatCannotBeDialed(t *testing.T) {
	swapTunnelDialer(t, func(context.Context, string) (net.Conn, string, error) {
		return nil, "JP-03", errors.New("connection refused")
	})

	if _, err := newTunnelClient().ExchangeContext(context.Background(), httpsQuery("example.com")); err == nil {
		t.Fatal("a dial failure must be reported")
	}
	if tunneldns.Supported("JP-03") {
		t.Fatal("a node that could not be dialed must be remembered")
	}
}

// An rcode that is not a refusal is the server answering. NXDOMAIN for a
// service binding is a real answer and must not be second-guessed by a public
// resolver, nor cost the node its standing.
func TestTunnelClientKeepsARealAnswer(t *testing.T) {
	swapTunnelDialer(t, func(context.Context, string) (net.Conn, string, error) {
		conn, _ := serveOneQuery(t, func(request *D.Msg) *D.Msg {
			response := new(D.Msg)
			response.SetRcode(request, D.RcodeNameError)
			return response
		})
		return conn, "JP-01", nil
	})

	msg, err := newTunnelClient().ExchangeContext(context.Background(), httpsQuery("nope.example"))
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if msg.Rcode != D.RcodeNameError {
		t.Fatalf("rcode = %s, want NXDOMAIN", D.RcodeToString[msg.Rcode])
	}
	if !tunneldns.Supported("JP-01") {
		t.Fatal("answering NXDOMAIN is still answering")
	}
}

// The public resolver is only reached when the node could not answer. The two
// are never asked together -- not even started together -- so a domain the
// server answered for is never sent anywhere else. Racing them would leak
// every queried name to the public resolver regardless of who won.
func TestTunnelFirstClientFallsBackOnlyOnFailure(t *testing.T) {
	served := new(D.Msg)
	public := &countingClient{msg: new(D.Msg), entered: make(chan struct{}, 1)}

	client := newTunnelFirstClient(&countingClient{msg: served}, public)
	got, err := client.ExchangeContext(context.Background(), httpsQuery("example.com"))
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if got != served {
		t.Fatal("the server's answer must be used")
	}
	// A parallel query would have been started before the tunnel returned, so
	// give one that was started time to arrive rather than assuming it has.
	select {
	case <-public.entered:
		t.Fatal("the public resolver must not see a domain the server answered for")
	case <-time.After(200 * time.Millisecond):
	}
	if got := public.calls.Load(); got != 0 {
		t.Fatalf("public calls = %d, want 0", got)
	}

	failing := &countingClient{err: errors.New("unsupported")}
	client = newTunnelFirstClient(failing, public)
	if _, err := client.ExchangeContext(context.Background(), httpsQuery("example.com")); err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if got := public.calls.Load(); got != 1 {
		t.Fatalf("public calls = %d, want 1", got)
	}
}

// A cancelled context is not a node's fault, and there is nothing to fall back
// to with it.
func TestTunnelFirstClientDoesNotFallBackOnCancellation(t *testing.T) {
	public := &countingClient{msg: new(D.Msg)}
	client := newTunnelFirstClient(&countingClient{err: context.Canceled}, public)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ExchangeContext(ctx, httpsQuery("example.com")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := public.calls.Load(); got != 0 {
		t.Fatal("a cancelled query must not be re-sent to the public resolver")
	}
}

func TestTunnelClientAddress(t *testing.T) {
	if got := newTunnelClient().Address(); got != "tunnel://"+C.TunnelDNSAddress {
		t.Fatalf("address = %q", got)
	}
}

type countingClient struct {
	msg     *D.Msg
	err     error
	calls   atomic.Int32
	entered chan struct{}
}

func (c *countingClient) ExchangeContext(_ context.Context, _ *D.Msg) (*D.Msg, error) {
	c.calls.Add(1)
	if c.entered != nil {
		select {
		case c.entered <- struct{}{}:
		default:
		}
	}
	return c.msg, c.err
}
func (c *countingClient) Address() string  { return "counting" }
func (c *countingClient) ResetConnection() {}

// stubResolver stands in for the fake-IP service resolver.
type stubResolver struct {
	resolver.Resolver
	msg   *D.Msg
	err   error
	calls atomic.Int32
}

func (r *stubResolver) ExchangeContext(_ context.Context, _ *D.Msg) (*D.Msg, error) {
	r.calls.Add(1)
	return r.msg, r.err
}

// A service resolver that could not be reached must not be worse than having
// none configured. Before this, one unreachable resolver turned every
// SVCB/HTTPS query into a failure -- so configuring it made things strictly
// worse than leaving it out.
func TestFakeIPFallsThroughWhenTheServiceResolverFails(t *testing.T) {
	fakePool := newTestFakeIPPool(t, "198.18.0.0/16")
	service := &stubResolver{err: errors.New("no node could answer")}

	ordinary := new(D.Msg)
	ordinary.Answer = []D.RR{mustRR(t, `example.com. 60 IN HTTPS 1 . alpn="h3"`)}
	nextCalls := 0
	next := func(_ *icontext.DNSContext, r *D.Msg) (*D.Msg, error) {
		nextCalls++
		reply := ordinary.Copy()
		reply.SetReply(r)
		reply.Answer = ordinary.Answer
		return reply, nil
	}

	handler := withFakeIP(&fakeip.Skipper{}, fakePool, nil, 1, service)(next)

	query := new(D.Msg)
	query.SetQuestion("example.com.", D.TypeHTTPS)
	msg, err := handler(icontext.NewDNSContext(context.Background()), query)
	if err != nil {
		t.Fatalf("the query must not fail because the service resolver did: %v", err)
	}
	if msg == nil {
		t.Fatal("the ordinary path must have produced an answer")
	}
	if service.calls.Load() != 1 {
		t.Fatalf("service resolver calls = %d, want 1", service.calls.Load())
	}
	if nextCalls != 1 {
		t.Fatalf("ordinary path calls = %d, want 1", nextCalls)
	}
}

// A service resolver that answered is used; the ordinary path is not consulted
// to second-guess it.
func TestFakeIPUsesTheServiceResolverWhenItAnswers(t *testing.T) {
	fakePool := newTestFakeIPPool(t, "198.18.0.0/16")

	served := new(D.Msg)
	served.Answer = []D.RR{mustRR(t, `example.com. 60 IN HTTPS 1 . alpn="h2" ipv4hint=192.0.2.10`)}
	service := &stubResolver{msg: served}

	nextCalls := 0
	next := func(_ *icontext.DNSContext, r *D.Msg) (*D.Msg, error) {
		nextCalls++
		return r.Copy(), nil
	}

	handler := withFakeIP(&fakeip.Skipper{}, fakePool, nil, 1, service)(next)

	query := new(D.Msg)
	query.SetQuestion("example.com.", D.TypeHTTPS)
	msg, err := handler(icontext.NewDNSContext(context.Background()), query)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if nextCalls != 0 {
		t.Fatal("an answered query must not also go down the ordinary path")
	}
	if len(msg.Answer) != 1 {
		t.Fatalf("answers = %d, want the served record", len(msg.Answer))
	}
	// The hint is rewritten to a fake IP; the rest of the record is the
	// server's, untouched.
	https, ok := msg.Answer[0].(*D.HTTPS)
	if !ok {
		t.Fatalf("answer = %T, want an HTTPS record", msg.Answer[0])
	}
	var sawFakeHint bool
	for _, value := range https.Value {
		if hint, is := value.(*D.SVCBIPv4Hint); is {
			for _, ip := range hint.Hint {
				addr, _ := netip.AddrFromSlice(ip.To4())
				if fakePool.IPNet().Contains(addr) {
					sawFakeHint = true
				}
			}
		}
	}
	if !sawFakeHint {
		t.Fatal("the address hint must be rewritten into the fake-IP pool")
	}
}

func mustRR(t *testing.T, text string) D.RR {
	t.Helper()
	rr, err := D.NewRR(text)
	if err != nil {
		t.Fatalf("rr: %v", err)
	}
	return rr
}
