package dns

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/metacubex/mihomo/common/lru"
	"github.com/metacubex/mihomo/common/singleflight"
	"github.com/metacubex/mihomo/component/tunneldns"
	"github.com/metacubex/mihomo/tunnel"
	D "github.com/miekg/dns"
)

// Experimental EDNS option shared with Xray. Version 1 requests an HTTPS
// answer plus the server's actual address lookup in Additional. An echoed
// option acknowledges a complete bundle, including an empty address family.
const domainBundleOption = 65001

type domainKey struct{ node, host string }
type domainClient struct {
	public  dnsClient
	bundles *tunneldns.Registry
	cache   *lru.LruCache[domainKey, *D.Msg]
	group   singleflight.Group[*D.Msg]
	prepare func(string) (string, func(context.Context) (net.Conn, error), error)
}

func newDomainClient(public dnsClient, size int) *domainClient {
	if size <= 0 {
		size = 1024
	}
	return &domainClient{public: public, bundles: tunneldns.NewRegistry(), cache: lru.New(lru.WithSize[domainKey, *D.Msg](size)), prepare: tunnel.PrepareTunnelDNS}
}

func (c *domainClient) ExchangeContext(ctx context.Context, request *D.Msg) (*D.Msg, error) {
	if len(request.Question) != 1 || request.Question[0].Qclass != D.ClassINET {
		return nil, errors.New("domain query needs one IN question")
	}
	q := request.Question[0]
	addressQuery := q.Qtype == D.TypeA || q.Qtype == D.TypeAAAA
	host := strings.ToLower(strings.TrimSuffix(q.Name, "."))
	node, dial, err := c.prepare(host)
	if err != nil {
		if addressQuery {
			return nil, err
		}
		return c.publicExchange(ctx, request)
	}
	key := domainKey{node, host}
	if !c.bundles.Supported(node) {
		if addressQuery {
			return nil, errors.New("server does not support domain bundles")
		}
		return c.exchange(ctx, node, host, dial, request)
	}
	if q.Qtype != D.TypeA && q.Qtype != D.TypeAAAA && q.Qtype != D.TypeHTTPS {
		return c.exchange(ctx, node, host, dial, request)
	}
	project := func(msg *D.Msg, expiry time.Time) *D.Msg {
		msg = msg.Copy()
		if !expiry.IsZero() {
			setMsgTTL(msg, uint32(max(0, time.Until(expiry)/time.Second)))
		}
		response := new(D.Msg)
		response.SetReply(request)
		response.RecursionAvailable = true
		response.Rcode = msg.Rcode
		if q.Qtype == D.TypeHTTPS {
			response.Answer = msg.Answer
			response.Ns = msg.Ns
		} else {
			// Keep the complete bundle internally, including its TTL when the
			// requested family is empty. withFakeIP never exposes these addresses.
			response.Extra = msg.Extra
			for _, rr := range msg.Extra {
				if rr.Header().Rrtype == q.Qtype && strings.EqualFold(rr.Header().Name, D.Fqdn(host)) {
					response.Answer = append(response.Answer, rr)
				}
			}
		}
		return response
	}
	if msg, expiry, ok := c.cache.GetWithExpire(key); ok && time.Now().Before(expiry) {
		return project(msg, expiry), nil
	}
	ch := c.group.DoChan(node+"\x00"+host, func() (*D.Msg, error) {
		// Bound shared work independently of the first caller's cancellation.
		work, cancel := context.WithTimeout(context.Background(), tunnelExchangeTimeout)
		defer cancel()
		if msg, expiry, ok := c.cache.GetWithExpire(key); ok && time.Now().Before(expiry) {
			copy := msg.Copy()
			setMsgTTL(copy, uint32(max(0, time.Until(expiry)/time.Second)))
			return copy, nil
		}
		query := new(D.Msg)
		query.SetQuestion(D.Fqdn(host), D.TypeHTTPS)
		query.SetEdns0(1232, false)
		query.IsEdns0().Option = append(query.IsEdns0().Option, &D.EDNS0_LOCAL{Code: domainBundleOption, Data: []byte{1}})
		msg, err := c.exchange(work, node, host, dial, query)
		if err != nil {
			return nil, err
		}
		if !hasDomainBundle(msg) {
			if msg.Rcode == D.RcodeSuccess {
				c.bundles.MarkUnsupported(node)
			}
			return nil, errors.New("server does not support domain bundles")
		}
		if msg.Rcode != D.RcodeSuccess || msg.Truncated {
			return nil, errors.New("incomplete domain bundle")
		}
		ttl := uint32(^uint32(0))
		foundAddress := false
		for _, rr := range append(append([]D.RR{}, msg.Answer...), msg.Extra...) {
			if rr.Header().Rrtype == D.TypeOPT {
				continue
			}
			ttl = min(ttl, rr.Header().Ttl)
			if (rr.Header().Rrtype == D.TypeA || rr.Header().Rrtype == D.TypeAAAA) && strings.EqualFold(rr.Header().Name, D.Fqdn(host)) {
				foundAddress = true
			}
		}
		if !foundAddress {
			return nil, errors.New("domain bundle has no real address")
		}
		// The entire entry expires together; never serve stale service metadata.
		setMsgTTL(msg, ttl)
		if ttl > 0 {
			c.cache.SetWithExpire(key, msg.Copy(), time.Now().Add(time.Duration(ttl)*time.Second))
		}
		return msg, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			if addressQuery {
				return nil, result.Err
			}
			// Older servers remain usable for ordinary record queries. They are not
			// marked unsupported just because they did not acknowledge this extension.
			return c.exchange(ctx, node, host, dial, request)
		}
		return project(result.Val, time.Time{}), nil
	}
}

func hasDomainBundle(msg *D.Msg) bool {
	if opt := msg.IsEdns0(); opt != nil {
		for _, option := range opt.Option {
			if local, ok := option.(*D.EDNS0_LOCAL); ok && local.Code == domainBundleOption && string(local.Data) == "\x01" {
				return true
			}
		}
	}
	return false
}

func (c *domainClient) exchange(ctx context.Context, node, host string, dial func(context.Context) (net.Conn, error), request *D.Msg) (*D.Msg, error) {
	conn, err := dial(ctx)
	if err == nil {
		defer conn.Close()
		var msg *D.Msg
		msg, err = exchangeOverConn(ctx, conn, request)
		if err == nil && msg.Rcode != D.RcodeRefused && msg.Rcode != D.RcodeNotImplemented {
			tunneldns.MarkSupported(node)
			return msg, nil
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil {
		err = errors.New("tunnel DNS refused query")
	}
	markTunnelDNSUnsupported(node, host, err)
	return c.publicExchange(ctx, request)
}

func (c *domainClient) publicExchange(ctx context.Context, request *D.Msg) (*D.Msg, error) {
	request = request.Copy()
	if opt := request.IsEdns0(); opt != nil {
		options := opt.Option[:0]
		for _, option := range opt.Option {
			if option.Option() != domainBundleOption {
				options = append(options, option)
			}
		}
		opt.Option = options
	}
	return c.public.ExchangeContext(ctx, request)
}
