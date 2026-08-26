package dns

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/metacubex/mihomo/component/fakeip"
	C "github.com/metacubex/mihomo/constant"
	icontext "github.com/metacubex/mihomo/context"

	D "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func TestRewriteFakeIPServiceBindings(t *testing.T) {
	fakePool := newTestFakeIPPool(t, "198.18.0.0/16")
	fakePool6 := newTestFakeIPPool(t, "2001:2::/120")
	unknownKey := D.SVCBKey(65400)

	https := &D.HTTPS{SVCB: D.SVCB{
		Hdr:      testRRHeader("example.com.", D.TypeHTTPS),
		Priority: 1,
		Target:   ".",
		Value: []D.SVCBKeyValue{
			&D.SVCBMandatory{Code: []D.SVCBKey{D.SVCB_ALPN, D.SVCB_IPV4HINT, D.SVCB_ECHCONFIG, D.SVCB_IPV6HINT, unknownKey}},
			&D.SVCBAlpn{Alpn: []string{"h3", "h2"}},
			&D.SVCBNoDefaultAlpn{},
			&D.SVCBPort{Port: 8443},
			&D.SVCBIPv4Hint{Hint: []net.IP{net.ParseIP("192.0.2.10").To4(), net.ParseIP("192.0.2.11").To4()}},
			&D.SVCBECHConfig{ECH: []byte{0x01, 0x02, 0x03}},
			&D.SVCBIPv6Hint{Hint: []net.IP{net.ParseIP("2001:db8::10")}},
			&D.SVCBLocal{KeyCode: unknownKey, Data: []byte("preserved")},
		},
	}}
	relatedSignature := testRRSIG("example.com.", D.TypeHTTPS)
	unrelatedSignature := testRRSIG("example.com.", D.TypeA)
	msg := &D.Msg{
		MsgHdr: D.MsgHdr{AuthenticatedData: true},
		Answer: []D.RR{https, relatedSignature, unrelatedSignature},
		Ns:     []D.RR{testRRSIG("example.com.", D.TypeHTTPS)},
		Extra:  []D.RR{testRRSIG("example.com.", D.TypeHTTPS)},
	}

	require.True(t, rewriteFakeIPServiceBindings(msg, fakePool, fakePool6, 1))
	require.False(t, msg.AuthenticatedData)
	require.False(t, hasCoveredRRSIG(msg.Answer, "example.com.", D.TypeHTTPS))
	require.False(t, hasCoveredRRSIG(msg.Ns, "example.com.", D.TypeHTTPS))
	require.False(t, hasCoveredRRSIG(msg.Extra, "example.com.", D.TypeHTTPS))
	require.True(t, hasCoveredRRSIG(msg.Answer, "example.com.", D.TypeA))

	rewritten := msg.Answer[0].(*D.HTTPS)
	require.Equal(t, uint16(1), rewritten.Priority)
	require.Equal(t, ".", rewritten.Target)
	require.Equal(t, uint32(1), rewritten.Hdr.Ttl)
	values := serviceValuesByKey(rewritten.Value)

	mandatory := values[D.SVCB_MANDATORY].(*D.SVCBMandatory)
	require.Equal(t, []D.SVCBKey{D.SVCB_ALPN, D.SVCB_IPV4HINT, D.SVCB_ECHCONFIG, D.SVCB_IPV6HINT, unknownKey}, mandatory.Code)
	require.Equal(t, []string{"h3", "h2"}, values[D.SVCB_ALPN].(*D.SVCBAlpn).Alpn)
	require.IsType(t, &D.SVCBNoDefaultAlpn{}, values[D.SVCB_NO_DEFAULT_ALPN])
	require.Equal(t, uint16(8443), values[D.SVCB_PORT].(*D.SVCBPort).Port)
	require.Equal(t, []byte{0x01, 0x02, 0x03}, values[D.SVCB_ECHCONFIG].(*D.SVCBECHConfig).ECH)
	require.Equal(t, []byte("preserved"), values[unknownKey].(*D.SVCBLocal).Data)

	ipv4Hint := values[D.SVCB_IPV4HINT].(*D.SVCBIPv4Hint)
	require.Len(t, ipv4Hint.Hint, 1)
	require.Equal(t, "198.18.0.4", ipv4Hint.Hint[0].String())
	assertFakeIPLookBack(t, fakePool, ipv4Hint.Hint[0], "example.com")

	ipv6Hint := values[D.SVCB_IPV6HINT].(*D.SVCBIPv6Hint)
	require.Len(t, ipv6Hint.Hint, 1)
	require.Equal(t, "2001:2::4", ipv6Hint.Hint[0].String())
	assertFakeIPLookBack(t, fakePool6, ipv6Hint.Hint[0], "example.com")
}

func TestRewriteFakeIPServiceBindingsUsesEffectiveTarget(t *testing.T) {
	fakePool := newTestFakeIPPool(t, "198.18.0.0/16")
	fakePool6 := newTestFakeIPPool(t, "2001:2::/120")
	msg := &D.Msg{Answer: []D.RR{
		&D.HTTPS{SVCB: D.SVCB{
			Hdr:      testRRHeader("origin.example.", D.TypeHTTPS),
			Priority: 1,
			Target:   "edge.cdn.example.",
			Value: []D.SVCBKeyValue{
				&D.SVCBIPv4Hint{Hint: []net.IP{net.ParseIP("192.0.2.20").To4()}},
				&D.SVCBIPv6Hint{Hint: []net.IP{net.ParseIP("2001:db8::20")}},
			},
		}},
		&D.SVCB{
			Hdr:      testRRHeader("service.example.", D.TypeSVCB),
			Priority: 2,
			Target:   ".",
			Value: []D.SVCBKeyValue{
				&D.SVCBIPv4Hint{Hint: []net.IP{net.ParseIP("192.0.2.30").To4()}},
				&D.SVCBIPv6Hint{Hint: []net.IP{net.ParseIP("2001:db8::30")}},
			},
		},
	}}

	require.True(t, rewriteFakeIPServiceBindings(msg, fakePool, fakePool6, 1))
	https := msg.Answer[0].(*D.HTTPS)
	svcb := msg.Answer[1].(*D.SVCB)
	require.Equal(t, "edge.cdn.example.", https.Target)
	require.Equal(t, ".", svcb.Target)

	httpsValues := serviceValuesByKey(https.Value)
	assertFakeIPLookBack(t, fakePool, httpsValues[D.SVCB_IPV4HINT].(*D.SVCBIPv4Hint).Hint[0], "edge.cdn.example")
	assertFakeIPLookBack(t, fakePool6, httpsValues[D.SVCB_IPV6HINT].(*D.SVCBIPv6Hint).Hint[0], "edge.cdn.example")

	svcbValues := serviceValuesByKey(svcb.Value)
	assertFakeIPLookBack(t, fakePool, svcbValues[D.SVCB_IPV4HINT].(*D.SVCBIPv4Hint).Hint[0], "service.example")
	assertFakeIPLookBack(t, fakePool6, svcbValues[D.SVCB_IPV6HINT].(*D.SVCBIPv6Hint).Hint[0], "service.example")
}

func TestRewriteFakeIPServiceBindingsRemovesUnavailableFamilies(t *testing.T) {
	tests := []struct {
		name             string
		fakePool         *fakeip.Pool
		fakePool6        *fakeip.Pool
		wantMandatory    []D.SVCBKey
		wantIPv4Hint     bool
		wantIPv6Hint     bool
		wantMandatoryKey bool
	}{
		{
			name:             "IPv6 only",
			fakePool6:        newTestFakeIPPool(t, "2001:2::/120"),
			wantMandatory:    []D.SVCBKey{D.SVCB_ECHCONFIG, D.SVCB_IPV6HINT},
			wantIPv6Hint:     true,
			wantMandatoryKey: true,
		},
		{
			name:             "IPv4 only",
			fakePool:         newTestFakeIPPool(t, "198.18.0.0/16"),
			wantMandatory:    []D.SVCBKey{D.SVCB_IPV4HINT, D.SVCB_ECHCONFIG},
			wantIPv4Hint:     true,
			wantMandatoryKey: true,
		},
		{
			name:             "no fake pools",
			wantMandatory:    []D.SVCBKey{D.SVCB_ECHCONFIG},
			wantMandatoryKey: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			https := &D.HTTPS{SVCB: D.SVCB{
				Hdr:      testRRHeader("example.com.", D.TypeHTTPS),
				Priority: 1,
				Target:   ".",
				Value: []D.SVCBKeyValue{
					&D.SVCBMandatory{Code: []D.SVCBKey{D.SVCB_IPV4HINT, D.SVCB_ECHCONFIG, D.SVCB_IPV6HINT}},
					&D.SVCBIPv4Hint{Hint: []net.IP{net.ParseIP("192.0.2.10").To4()}},
					&D.SVCBECHConfig{ECH: []byte{0x01}},
					&D.SVCBIPv6Hint{Hint: []net.IP{net.ParseIP("2001:db8::10")}},
				},
			}}
			msg := &D.Msg{Answer: []D.RR{https}}

			require.True(t, rewriteFakeIPServiceBindings(msg, test.fakePool, test.fakePool6, 1))
			values := serviceValuesByKey(https.Value)
			require.Equal(t, test.wantIPv4Hint, values[D.SVCB_IPV4HINT] != nil)
			require.Equal(t, test.wantIPv6Hint, values[D.SVCB_IPV6HINT] != nil)
			require.Equal(t, []byte{0x01}, values[D.SVCB_ECHCONFIG].(*D.SVCBECHConfig).ECH)
			require.Equal(t, test.wantMandatoryKey, values[D.SVCB_MANDATORY] != nil)
			if test.wantMandatoryKey {
				require.Equal(t, test.wantMandatory, values[D.SVCB_MANDATORY].(*D.SVCBMandatory).Code)
			}
		})
	}
}

func TestRewriteFakeIPServiceBindingsLeavesAliasModeUntouched(t *testing.T) {
	fakePool := newTestFakeIPPool(t, "198.18.0.0/16")
	fakePool6 := newTestFakeIPPool(t, "2001:2::/120")
	alias := &D.HTTPS{SVCB: D.SVCB{
		Hdr:      testRRHeader("origin.example.", D.TypeHTTPS),
		Priority: 0,
		Target:   "edge.example.",
		Value: []D.SVCBKeyValue{
			&D.SVCBIPv4Hint{Hint: []net.IP{net.ParseIP("192.0.2.40").To4()}},
			&D.SVCBECHConfig{ECH: []byte{0x01, 0x02}},
			&D.SVCBIPv6Hint{Hint: []net.IP{net.ParseIP("2001:db8::40")}},
		},
	}}
	msg := &D.Msg{
		MsgHdr: D.MsgHdr{AuthenticatedData: true},
		Answer: []D.RR{alias, testRRSIG("origin.example.", D.TypeHTTPS)},
	}

	require.False(t, rewriteFakeIPServiceBindings(msg, fakePool, fakePool6, 1))
	require.True(t, msg.AuthenticatedData)
	require.True(t, hasCoveredRRSIG(msg.Answer, "origin.example.", D.TypeHTTPS))
	require.Equal(t, uint32(300), alias.Hdr.Ttl)
	require.Equal(t, "192.0.2.40", alias.Value[0].(*D.SVCBIPv4Hint).Hint[0].String())
	require.IsType(t, &D.SVCBECHConfig{}, alias.Value[1])
	require.Equal(t, "2001:db8::40", alias.Value[2].(*D.SVCBIPv6Hint).Hint[0].String())
	_, ipv4Allocated := fakePool.LookBack(netip.MustParseAddr("198.18.0.4"))
	_, ipv6Allocated := fakePool6.LookBack(netip.MustParseAddr("2001:2::4"))
	require.False(t, ipv4Allocated)
	require.False(t, ipv6Allocated)
}

func TestWithFakeIPForwardsAndSynthesizesServiceQueries(t *testing.T) {
	for _, qType := range []uint16{D.TypeHTTPS, D.TypeSVCB} {
		t.Run(D.TypeToString[qType], func(t *testing.T) {
			fakePool := newTestFakeIPPool(t, "198.18.0.0/16")
			request := new(D.Msg)
			request.SetQuestion("example.com.", qType)
			upstream := &D.Msg{Answer: []D.RR{testServiceRecord(qType, "example.com.")}}
			called := false
			next := func(ctx *icontext.DNSContext, _ *D.Msg) (*D.Msg, error) {
				called = true
				ctx.SetType(icontext.DNSTypeRaw)
				return upstream, nil
			}
			ctx := icontext.NewDNSContext(context.Background())

			response, err := withFakeIP(&fakeip.Skipper{}, fakePool, nil, 1, nil)(next)(ctx, request)
			require.NoError(t, err)
			require.True(t, called)
			require.Equal(t, icontext.DNSTypeFakeIP, ctx.Type())
			require.Len(t, response.Answer, 1)
			require.NotSame(t, upstream, response)

			responseValues := serviceRecordValues(response.Answer[0])
			require.Equal(t, []byte{0x01}, responseValues[D.SVCB_ECHCONFIG].(*D.SVCBECHConfig).ECH)
			require.Equal(t, "198.18.0.4", responseValues[D.SVCB_IPV4HINT].(*D.SVCBIPv4Hint).Hint[0].String())
			upstreamValues := serviceRecordValues(upstream.Answer[0])
			require.Contains(t, upstreamValues, D.SVCB_ECHCONFIG)
			require.Equal(t, "192.0.2.50", upstreamValues[D.SVCB_IPV4HINT].(*D.SVCBIPv4Hint).Hint[0].String())
		})
	}
}

type recordingServiceClient struct {
	calls    []uint16
	response *D.Msg
}

func (c *recordingServiceClient) ExchangeContext(_ context.Context, request *D.Msg) (*D.Msg, error) {
	c.calls = append(c.calls, request.Question[0].Qtype)
	return c.response.Copy(), nil
}

func (*recordingServiceClient) Address() string  { return "test://fake-ip-service" }
func (*recordingServiceClient) ResetConnection() {}

func TestWithFakeIPUsesDedicatedResolverOnlyForServiceQueries(t *testing.T) {
	fakePool := newTestFakeIPPool(t, "198.18.0.0/16")
	client := &recordingServiceClient{response: &D.Msg{Answer: []D.RR{testServiceRecord(D.TypeHTTPS, "example.com.")}}}
	serviceResolver := NewResolverFromClient(client)
	next := func(_ *icontext.DNSContext, _ *D.Msg) (*D.Msg, error) {
		t.Fatal("main nameserver was called")
		return nil, nil
	}
	handler := withFakeIP(&fakeip.Skipper{}, fakePool, nil, 1, serviceResolver)(next)

	httpsRequest := new(D.Msg)
	httpsRequest.SetQuestion("example.com.", D.TypeHTTPS)
	httpsRequest.Id = 100
	httpsResponse, err := handler(icontext.NewDNSContext(context.Background()), httpsRequest)
	require.NoError(t, err)
	require.Equal(t, uint16(100), httpsResponse.Id)
	require.Equal(t, []uint16{D.TypeHTTPS}, client.calls)
	require.Contains(t, serviceRecordValues(httpsResponse.Answer[0]), D.SVCB_ECHCONFIG)
	httpsRequest.Id = 200
	httpsResponse, err = handler(icontext.NewDNSContext(context.Background()), httpsRequest)
	require.NoError(t, err)
	require.Equal(t, uint16(200), httpsResponse.Id)

	aRequest := new(D.Msg)
	aRequest.SetQuestion("example.com.", D.TypeA)
	aResponse, err := handler(icontext.NewDNSContext(context.Background()), aRequest)
	require.NoError(t, err)
	require.Equal(t, []uint16{D.TypeHTTPS}, client.calls)
	require.Len(t, aResponse.Answer, 1)
	require.IsType(t, &D.A{}, aResponse.Answer[0])
}

func TestWithFakeIPRealIPSkipPreservesServiceBindings(t *testing.T) {
	fakePool := newTestFakeIPPool(t, "198.18.0.0/16")
	request := new(D.Msg)
	request.SetQuestion("example.com.", D.TypeHTTPS)
	upstream := &D.Msg{MsgHdr: D.MsgHdr{AuthenticatedData: true}, Answer: []D.RR{
		testServiceRecord(D.TypeHTTPS, "example.com."),
		testRRSIG("example.com.", D.TypeHTTPS),
	}}
	next := func(ctx *icontext.DNSContext, _ *D.Msg) (*D.Msg, error) {
		ctx.SetType(icontext.DNSTypeRaw)
		return upstream, nil
	}
	ctx := icontext.NewDNSContext(context.Background())

	response, err := withFakeIP(&fakeip.Skipper{Mode: C.FilterWhiteList}, fakePool, nil, 1, nil)(next)(ctx, request)
	require.NoError(t, err)
	require.Same(t, upstream, response)
	require.Equal(t, icontext.DNSTypeRaw, ctx.Type())
	require.True(t, response.AuthenticatedData)
	require.True(t, hasCoveredRRSIG(response.Answer, "example.com.", D.TypeHTTPS))
	values := serviceRecordValues(response.Answer[0])
	require.Contains(t, values, D.SVCB_ECHCONFIG)
	require.Equal(t, "192.0.2.50", values[D.SVCB_IPV4HINT].(*D.SVCBIPv4Hint).Hint[0].String())
}

func TestSynthesizedServiceBindingsRoundTrip(t *testing.T) {
	for _, rrType := range []uint16{D.TypeHTTPS, D.TypeSVCB} {
		t.Run(D.TypeToString[rrType], func(t *testing.T) {
			fakePool := newTestFakeIPPool(t, "198.18.0.0/16")
			msg := &D.Msg{
				MsgHdr:   D.MsgHdr{Id: 7, Response: true, AuthenticatedData: true},
				Question: []D.Question{{Name: "example.com.", Qtype: rrType, Qclass: D.ClassINET}},
				Answer:   []D.RR{testServiceRecord(rrType, "example.com.")},
			}
			require.True(t, rewriteFakeIPServiceBindings(msg, fakePool, nil, 1))

			wire, err := msg.Pack()
			require.NoError(t, err)
			var decoded D.Msg
			require.NoError(t, decoded.Unpack(wire))
			require.False(t, decoded.AuthenticatedData)
			require.Len(t, decoded.Answer, 1)
			require.Equal(t, uint32(1), decoded.Answer[0].Header().Ttl)
			values := serviceRecordValues(decoded.Answer[0])
			require.Equal(t, []byte{0x01}, values[D.SVCB_ECHCONFIG].(*D.SVCBECHConfig).ECH)
			require.Equal(t, []string{"h3", "h2"}, values[D.SVCB_ALPN].(*D.SVCBAlpn).Alpn)
			require.Equal(t, "198.18.0.4", values[D.SVCB_IPV4HINT].(*D.SVCBIPv4Hint).Hint[0].String())
		})
	}
}

func newTestFakeIPPool(t *testing.T, prefix string) *fakeip.Pool {
	t.Helper()
	pool, err := fakeip.New(fakeip.Options{IPNet: netip.MustParsePrefix(prefix), Size: 32})
	require.NoError(t, err)
	return pool
}

func testRRHeader(name string, rrType uint16) D.RR_Header {
	return D.RR_Header{Name: name, Rrtype: rrType, Class: D.ClassINET, Ttl: 300}
}

func testRRSIG(name string, covered uint16) *D.RRSIG {
	return &D.RRSIG{Hdr: testRRHeader(name, D.TypeRRSIG), TypeCovered: covered}
}

func testServiceRecord(rrType uint16, name string) D.RR {
	svcb := D.SVCB{
		Hdr:      testRRHeader(name, rrType),
		Priority: 1,
		Target:   ".",
		Value: []D.SVCBKeyValue{
			&D.SVCBAlpn{Alpn: []string{"h3", "h2"}},
			&D.SVCBIPv4Hint{Hint: []net.IP{net.ParseIP("192.0.2.50").To4()}},
			&D.SVCBECHConfig{ECH: []byte{0x01}},
		},
	}
	if rrType == D.TypeHTTPS {
		return &D.HTTPS{SVCB: svcb}
	}
	return &svcb
}

func serviceRecordValues(record D.RR) map[D.SVCBKey]D.SVCBKeyValue {
	switch rr := record.(type) {
	case *D.HTTPS:
		return serviceValuesByKey(rr.Value)
	case *D.SVCB:
		return serviceValuesByKey(rr.Value)
	default:
		return nil
	}
}

func serviceValuesByKey(values []D.SVCBKeyValue) map[D.SVCBKey]D.SVCBKeyValue {
	result := make(map[D.SVCBKey]D.SVCBKeyValue, len(values))
	for _, value := range values {
		result[value.Key()] = value
	}
	return result
}

func assertFakeIPLookBack(t *testing.T, pool *fakeip.Pool, ip net.IP, wantHost string) {
	t.Helper()
	addr, ok := netip.AddrFromSlice(ip)
	require.True(t, ok)
	host, ok := pool.LookBack(addr)
	require.True(t, ok)
	require.Equal(t, wantHost, host)
}

func hasCoveredRRSIG(records []D.RR, name string, covered uint16) bool {
	for _, record := range records {
		signature, ok := record.(*D.RRSIG)
		if ok && signature.Hdr.Name == name && signature.TypeCovered == covered {
			return true
		}
	}
	return false
}
