package udp

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/metacubex/mihomo/transport/hysteria/obfs"
)

func TestObfsUDPConnPreservesOOBCapability(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer server.Close()

	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)

	wrapped := NewObfsUDPConn(client, obfs.NewDummyObfuscator())
	defer wrapped.Close()
	oobConn, ok := wrapped.(oobCapablePacketConn)
	require.True(t, ok)

	payload := []byte("ecn-oob")
	n, _, err := oobConn.WriteMsgUDP(payload, nil, server.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
	require.Equal(t, len(payload), n)

	buf := make([]byte, 64)
	n, clientAddr, err := server.ReadFromUDP(buf)
	require.NoError(t, err)
	require.Equal(t, payload, buf[:n])

	_, err = server.WriteToUDP(payload, clientAddr)
	require.NoError(t, err)
	n, _, _, _, err = oobConn.ReadMsgUDP(buf, make([]byte, oobBufferSize))
	require.NoError(t, err)
	require.Equal(t, payload, buf[:n])
}

type hopTestDialer struct {
	remote  *net.UDPAddr
	listens int
}

func (d *hopTestDialer) ListenPacket(net.Addr) (net.PacketConn, error) {
	d.listens++
	return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
}

func (d *hopTestDialer) Context() context.Context { return context.Background() }

func (d *hopTestDialer) RemoteAddr(string) (net.Addr, error) { return d.remote, nil }

func TestPortHopReusesECNConfiguredSocket(t *testing.T) {
	dialer := &hopTestDialer{remote: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 10000}}
	conn, err := NewObfsUDPHopClientPacketConn(
		dialer.remote.String(),
		fmt.Sprintf("%d,%d", dialer.remote.Port, dialer.remote.Port+1),
		time.Hour,
		nil,
		dialer,
	)
	require.NoError(t, err)
	defer conn.Close()

	hopConn, ok := conn.(*ObfsUDPHopClientPacketConnWithOOB)
	require.True(t, ok)
	localAddr := hopConn.LocalAddr().String()

	for i := 0; i < 8; i++ {
		hopConn.hop()
		require.Equal(t, localAddr, hopConn.LocalAddr().String())
	}
	require.Equal(t, 1, dialer.listens)
}
