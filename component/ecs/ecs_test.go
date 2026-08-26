package ecs

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsPublic(t *testing.T) {
	for _, addr := range []string{"1.2.3.4", "2001:db8::1"} {
		assert.True(t, isPublic(netip.MustParseAddr(addr)), addr)
	}
	for _, addr := range []string{"0.0.0.0", "127.0.0.1", "192.168.1.2", "10.0.0.1", "169.254.1.1", "100.64.0.1", "::1", "fe80::1"} {
		assert.False(t, isPublic(netip.MustParseAddr(addr)), addr)
	}
}

func TestSetupDisabledClearsPrefix(t *testing.T) {
	SetPrefixForTest(netip.MustParsePrefix("1.2.3.0/24"), netip.MustParsePrefix("2001:db8::/56"))
	Setup(false)
	assert.False(t, Prefix(true).IsValid())
	assert.False(t, Prefix(false).IsValid())
}

func TestPrefixPrefersRequestedFamily(t *testing.T) {
	v4 := netip.MustParsePrefix("1.2.3.0/24")
	v6 := netip.MustParsePrefix("2001:db8::/56")
	t.Cleanup(func() { SetPrefixForTest(netip.Prefix{}, netip.Prefix{}) })

	SetPrefixForTest(v4, v6)
	assert.Equal(t, v4, Prefix(true))
	assert.Equal(t, v6, Prefix(false))

	// only one family discovered: it is still better than no ECS at all
	SetPrefixForTest(v4, netip.Prefix{})
	assert.Equal(t, v4, Prefix(false))
	SetPrefixForTest(netip.Prefix{}, v6)
	assert.Equal(t, v6, Prefix(true))
}

// TestDiscoverSTUNAgainstLocalSTUN exercises a whole STUN round against a
// minimal responder, so the masking of the reported address is covered
// without reaching the network.
func TestDiscoverSTUNAgainstLocalSTUN(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	assert.NoError(t, err)
	defer server.Close()
	go serveSTUN(server, netip.MustParseAddr("203.0.113.77"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addr, err := discoverSTUN(ctx, true, []string{server.LocalAddr().String()})
	assert.NoError(t, err)
	assert.Equal(t, netip.MustParseAddr("203.0.113.77"), addr)
	assert.Equal(t, netip.MustParsePrefix("203.0.113.0/24"), maskPrefix(addr))
}

// TestDiscoverSTUNRejectsPrivateAddress guards the case where the STUN
// server is reached without traversing NAT, e.g. a LAN-local responder.
func TestDiscoverSTUNRejectsPrivateAddress(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	assert.NoError(t, err)
	defer server.Close()
	go serveSTUN(server, netip.MustParseAddr("192.168.8.8"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = discoverSTUN(ctx, true, []string{server.LocalAddr().String()})
	assert.ErrorIs(t, err, errNoPublicAddress)
}

const stunMagicCookie = 0x2112A442

// serveSTUN answers binding requests with a fixed XOR-MAPPED-ADDRESS.
func serveSTUN(conn net.PacketConn, mapped netip.Addr) {
	buffer := make([]byte, 1500)
	for {
		n, from, err := conn.ReadFrom(buffer)
		if err != nil {
			return
		}
		if n < 20 || binary.BigEndian.Uint16(buffer[0:2]) != 0x0001 {
			continue
		}
		var transactionID [12]byte
		copy(transactionID[:], buffer[8:20])
		_, _ = conn.WriteTo(bindingResponse(transactionID, mapped), from)
	}
}

func bindingResponse(transactionID [12]byte, mapped netip.Addr) []byte {
	address := mapped.As4()
	for i := range address {
		address[i] ^= byte(uint32(stunMagicCookie) >> (24 - 8*i))
	}

	attribute := make([]byte, 0, 12)
	attribute = binary.BigEndian.AppendUint16(attribute, 0x0020) // XOR-MAPPED-ADDRESS
	attribute = binary.BigEndian.AppendUint16(attribute, 8)      // value length
	attribute = append(attribute, 0, 0x01)                       // reserved, family IPv4
	attribute = binary.BigEndian.AppendUint16(attribute, 443^(stunMagicCookie>>16))
	attribute = append(attribute, address[:]...)

	message := make([]byte, 0, 20+len(attribute))
	message = binary.BigEndian.AppendUint16(message, 0x0101) // binding success response
	message = binary.BigEndian.AppendUint16(message, uint16(len(attribute)))
	message = binary.BigEndian.AppendUint32(message, stunMagicCookie)
	message = append(message, transactionID[:]...)
	message = append(message, attribute...)
	return message
}
