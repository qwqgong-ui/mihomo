package dns

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/metacubex/mihomo/component/tunneldns"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"

	D "github.com/miekg/dns"
)

// tunnelExchangeTimeout bounds one question asked through a proxy.
const tunnelExchangeTimeout = 5 * time.Second

// tunnelClient asks the proxy server that the queried domain's own traffic
// goes through, over that server's tunnel, and uses the answer it gives.
//
// The node is picked from the domain in the question, so the record comes from
// the resolver of the very server the connection will be built through. What
// travels inside the tunnel is an ordinary DNS message: nothing here encodes,
// frames or interprets anything of its own, and the reply parses with the same
// library every other answer does.
type tunnelClient struct{}

func newTunnelClient() dnsClient { return tunnelClient{} }

func (tunnelClient) Address() string { return "tunnel://" + C.TunnelDNSAddress }

func (tunnelClient) ResetConnection() {}

func (c tunnelClient) ExchangeContext(ctx context.Context, m *D.Msg) (*D.Msg, error) {
	domain := msgToDomain(m)
	if domain == "" {
		return nil, errors.New("tunnel DNS needs a domain to select a node with")
	}

	conn, node, err := dialTunnelDNS(ctx, domain)
	if err != nil {
		// A server that has not implemented the reserved destination routes it
		// out like any other name, where it fails to resolve. That failure is
		// the discovery, so remember it rather than paying for it per query.
		markTunnelDNSUnsupported(node, domain, err)
		return nil, err
	}
	defer conn.Close()

	msg, err := exchangeOverConn(ctx, conn, m)
	if err != nil {
		markTunnelDNSUnsupported(node, domain, err)
		return nil, err
	}

	switch msg.Rcode {
	case D.RcodeRefused, D.RcodeNotImplemented:
		// The destination is served, but not for this. Answered questions and
		// this one look the same from here, so stop asking this node at all.
		err := errors.New("tunnel DNS refused: " + D.RcodeToString[msg.Rcode])
		markTunnelDNSUnsupported(node, domain, err)
		return nil, err
	}

	tunneldns.MarkSupported(node)
	return msg, nil
}

func markTunnelDNSUnsupported(node, domain string, err error) {
	if node == "" {
		return
	}
	tunneldns.MarkUnsupported(node)
	log.Debugln("[DNS] %s cannot answer tunnel DNS for %s, falling back: %v", node, domain, err)
}

// exchangeOverConn speaks ordinary DNS over a stream, which miekg/dns already
// frames per RFC 1035 4.2.2. It only exists to make the exchange respect ctx,
// which miekg's own does not.
func exchangeOverConn(ctx context.Context, conn net.Conn, m *D.Msg) (*D.Msg, error) {
	type result struct {
		msg *D.Msg
		err error
	}
	ch := make(chan result, 1)
	go func() {
		client := &D.Client{Net: "tcp", Timeout: tunnelExchangeTimeout}
		msg, _, err := client.ExchangeWithConn(m, &D.Conn{Conn: conn})
		ch <- result{msg, err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case got := <-ch:
		if got.err != nil {
			return nil, got.err
		}
		if got.msg == nil {
			return nil, errors.New("tunnel DNS returned no message")
		}
		return got.msg, nil
	}
}

// tunnelFirstClient asks the proxy server first and only falls back when that
// server cannot answer at all.
//
// It is not a race and the two are not asked together. The server's record is
// the one the connection is actually built on, so it is worth waiting for; the
// public resolver exists for a server that has no such handler, and thanks to
// the per-node memory that costs one attempt per node rather than one per
// query. Asking both every time would also send every queried domain to the
// public resolver even when the server answered.
type tunnelFirstClient struct {
	tunnel dnsClient
	public dnsClient
}

func newTunnelFirstClient(tunnel, public dnsClient) dnsClient {
	return &tunnelFirstClient{tunnel: tunnel, public: public}
}

func (c *tunnelFirstClient) ExchangeContext(ctx context.Context, m *D.Msg) (*D.Msg, error) {
	if msg, err := c.tunnel.ExchangeContext(ctx, m); err == nil {
		return msg, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return c.public.ExchangeContext(ctx, m.Copy())
}

func (c *tunnelFirstClient) Address() string {
	return strings.Join([]string{c.tunnel.Address(), c.public.Address()}, ",")
}

func (c *tunnelFirstClient) ResetConnection() {
	c.tunnel.ResetConnection()
	c.public.ResetConnection()
}
