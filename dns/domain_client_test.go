package dns

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/fakeip"
	icontext "github.com/metacubex/mihomo/context"
	D "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func testBundleClient(t *testing.T) (*domainClient, *atomic.Int32, *string) {
	t.Helper()
	public := &recordingServiceClient{response: &D.Msg{}}
	client := newDomainClient(public, 10)
	calls := new(atomic.Int32)
	node := "JP"
	client.prepare = func(host string) (string, func(context.Context) (net.Conn, error), error) {
		selected := node
		return selected, func(context.Context) (net.Conn, error) {
			calls.Add(1)
			conn, _ := serveOneQuery(t, func(request *D.Msg) *D.Msg {
				response := new(D.Msg)
				response.SetReply(request)
				response.Answer = []D.RR{testServiceRecord(D.TypeHTTPS, D.Fqdn(host))}
				address := "192.0.2.1"
				if selected == "US" {
					address = "192.0.2.2"
				}
				rr, _ := D.NewRR(D.Fqdn(host) + " 20 IN A " + address)
				response.Extra = []D.RR{rr}
				response.SetEdns0(1232, false)
				response.IsEdns0().Option = []D.EDNS0{&D.EDNS0_LOCAL{Code: domainBundleOption, Data: []byte{1}}}
				return response
			})
			return conn, nil
		}, nil
	}
	return client, calls, &node
}

func TestDomainBundleAddressFirstNodeIsolationAndExpiry(t *testing.T) {
	client, calls, node := testBundleClient(t)
	request := new(D.Msg)
	request.SetQuestion("EXAMPLE.com.", D.TypeA)
	answer, err := client.ExchangeContext(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "192.0.2.1", answer.Answer[0].(*D.A).A.String())
	require.Equal(t, uint32(20), answer.Answer[0].Header().Ttl)
	service, err := client.ExchangeContext(t.Context(), httpsQuery("example.com"))
	require.NoError(t, err)
	require.Contains(t, serviceRecordValues(service.Answer[0]), D.SVCB_ECHCONFIG)
	require.EqualValues(t, 1, calls.Load())
	// Caller mutation must not poison the raw cached record.
	service.Answer[0].(*D.HTTPS).Value = nil
	service, err = client.ExchangeContext(t.Context(), httpsQuery("example.com"))
	require.NoError(t, err)
	require.NotEmpty(t, service.Answer[0].(*D.HTTPS).Value)
	*node = "US"
	answer, err = client.ExchangeContext(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "192.0.2.2", answer.Answer[0].(*D.A).A.String())
	require.EqualValues(t, 2, calls.Load())
	key := domainKey{"US", "example.com"}
	cached, ok := client.cache.Get(key)
	require.True(t, ok)
	client.cache.SetWithExpire(key, cached, time.Now().Add(-time.Second))
	_, err = client.ExchangeContext(t.Context(), httpsQuery("example.com"))
	require.NoError(t, err)
	require.EqualValues(t, 3, calls.Load())
}

func TestFakeIPFirstAddressWarmsServiceBundle(t *testing.T) {
	client, calls, _ := testBundleClient(t)
	pool := newTestFakeIPPool(t, "198.18.0.0/16")
	handler := withFakeIP(&fakeip.Skipper{}, pool, nil, 60, &Resolver{domainClient: client})(func(*icontext.DNSContext, *D.Msg) (*D.Msg, error) { t.Fatal("unexpected fallback"); return nil, nil })
	request := new(D.Msg)
	request.SetQuestion("example.com.", D.TypeA)
	answer, err := handler(icontext.NewDNSContext(t.Context()), request)
	require.NoError(t, err)
	require.Equal(t, "198.18.0.4", answer.Answer[0].(*D.A).A.String())
	require.Equal(t, uint32(20), answer.Answer[0].Header().Ttl)
	require.EqualValues(t, 1, calls.Load())
	service, err := handler(icontext.NewDNSContext(t.Context()), httpsQuery("example.com"))
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())
	require.Contains(t, serviceRecordValues(service.Answer[0]), D.SVCB_ECHCONFIG)
	cached, ok := client.cache.Get(domainKey{"JP", "example.com"})
	require.True(t, ok)
	require.Equal(t, "192.0.2.1", cached.Extra[0].(*D.A).A.String())
	require.Equal(t, "192.0.2.50", serviceRecordValues(cached.Answer[0])[D.SVCB_IPV4HINT].(*D.SVCBIPv4Hint).Hint[0].String())
	require.LessOrEqual(t, service.Answer[0].Header().Ttl, uint32(20))
}

func TestOldServerDoesNotCausePublicAddressQueries(t *testing.T) {
	public := &recordingServiceClient{response: &D.Msg{}}
	client := newDomainClient(public, 10)
	var calls atomic.Int32
	client.prepare = func(string) (string, func(context.Context) (net.Conn, error), error) {
		return "old", func(context.Context) (net.Conn, error) {
			calls.Add(1)
			conn, _ := serveOneQuery(t, func(request *D.Msg) *D.Msg {
				response := new(D.Msg)
				response.SetReply(request)
				response.Answer = []D.RR{testServiceRecord(D.TypeHTTPS, "example.com.")}
				return response
			})
			return conn, nil
		}, nil
	}
	request := new(D.Msg)
	request.SetQuestion("example.com.", D.TypeA)
	_, err := client.ExchangeContext(t.Context(), request)
	require.Error(t, err)
	_, err = client.ExchangeContext(t.Context(), request)
	require.Error(t, err)
	require.EqualValues(t, 1, calls.Load(), "old node should not be probed repeatedly")
	require.Empty(t, public.calls, "fake IP warmup must not resolve real addresses publicly")
	answer, err := client.ExchangeContext(t.Context(), httpsQuery("example.com"))
	require.NoError(t, err)
	require.Len(t, answer.Answer, 1)
	require.EqualValues(t, 2, calls.Load())
}

func TestFakeIPv6UsesBundleTTLWhenServerOnlyHasIPv4(t *testing.T) {
	client, _, _ := testBundleClient(t)
	pool := newTestFakeIPPool(t, "2001:2::/64")
	handler := withFakeIP(&fakeip.Skipper{}, nil, pool, 60, &Resolver{domainClient: client})(nil)
	request := new(D.Msg)
	request.SetQuestion("example.com.", D.TypeAAAA)
	answer, err := handler(icontext.NewDNSContext(t.Context()), request)
	require.NoError(t, err)
	require.Len(t, answer.Answer, 1)
	require.Equal(t, uint32(20), answer.Answer[0].Header().Ttl)
	require.Empty(t, answer.Extra)
}
