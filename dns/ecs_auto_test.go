package dns

import (
	"context"
	"net/netip"
	"testing"

	"github.com/metacubex/mihomo/component/ecs"

	D "github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
)

type recordingClient struct {
	last *D.Msg
}

func (c *recordingClient) ExchangeContext(ctx context.Context, m *D.Msg) (*D.Msg, error) {
	c.last = m
	return m, nil
}

func (c *recordingClient) Address() string { return "test" }

func (c *recordingClient) ResetConnection() {}

func subnetOf(t *testing.T, m *D.Msg) *D.EDNS0_SUBNET {
	t.Helper()
	opt := m.IsEdns0()
	if opt == nil {
		return nil
	}
	for _, option := range opt.Option {
		if subnet, ok := option.(*D.EDNS0_SUBNET); ok {
			return subnet
		}
	}
	return nil
}

func TestClientWithAutoEdns0Subnet(t *testing.T) {
	raw := &recordingClient{}
	c := wrapClientWithEdns0Subnet(raw, map[string]string{"ecs": "auto"})
	assert.IsType(t, clientWithEdns0Subnet{}, c)

	query := new(D.Msg)
	query.SetQuestion("example.com.", D.TypeA)

	// nothing discovered yet: the query must go out untouched
	ecs.Setup(false)
	_, err := c.ExchangeContext(context.Background(), query)
	assert.NoError(t, err)
	assert.Nil(t, subnetOf(t, raw.last))

	ecs.SetPrefixForTest(netip.MustParsePrefix("1.2.3.0/24"), netip.MustParsePrefix("2001:db8::/56"))
	t.Cleanup(func() { ecs.SetPrefixForTest(netip.Prefix{}, netip.Prefix{}) })

	_, err = c.ExchangeContext(context.Background(), query)
	assert.NoError(t, err)
	subnet := subnetOf(t, raw.last)
	if assert.NotNil(t, subnet) {
		assert.Equal(t, uint16(1), subnet.Family)
		assert.Equal(t, uint8(24), subnet.SourceNetmask)
		assert.Equal(t, "1.2.3.0", subnet.Address.String())
	}
	// the caller's message is never mutated
	assert.Nil(t, subnetOf(t, query))

	// an AAAA question gets the IPv6 subnet instead
	query6 := new(D.Msg)
	query6.SetQuestion("example.com.", D.TypeAAAA)
	_, err = c.ExchangeContext(context.Background(), query6)
	assert.NoError(t, err)
	subnet = subnetOf(t, raw.last)
	if assert.NotNil(t, subnet) {
		assert.Equal(t, uint16(2), subnet.Family)
		assert.Equal(t, uint8(56), subnet.SourceNetmask)
		assert.Equal(t, "2001:db8::", subnet.Address.String())
	}
}

func TestClientWithStaticEdns0SubnetStillWorks(t *testing.T) {
	raw := &recordingClient{}
	c := wrapClientWithEdns0Subnet(raw, map[string]string{"ecs": "5.6.7.8"})

	query := new(D.Msg)
	query.SetQuestion("example.com.", D.TypeA)
	_, err := c.ExchangeContext(context.Background(), query)
	assert.NoError(t, err)
	subnet := subnetOf(t, raw.last)
	if assert.NotNil(t, subnet) {
		assert.Equal(t, uint8(32), subnet.SourceNetmask)
		assert.Equal(t, "5.6.7.8", subnet.Address.String())
	}
}

func TestWrapClientWithInvalidEcsIsPassthrough(t *testing.T) {
	raw := &recordingClient{}
	assert.Equal(t, dnsClient(raw), wrapClientWithEdns0Subnet(raw, map[string]string{"ecs": "not-an-ip"}))
}
