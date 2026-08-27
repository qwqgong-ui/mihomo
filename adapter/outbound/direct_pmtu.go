package outbound

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/component/dialer"
	C "github.com/metacubex/mihomo/constant"
)

// pathMTUCacheTTL bounds how often a sender that ignores what it is told can
// make us ask the kernel again.
const pathMTUCacheTTL = 5 * time.Second

// pathMTUPacketConn turns the EMSGSIZE a write gets when the datagram exceeds
// the path MTU into an error that names the MTU, so the tunnel can pass it back
// to whoever sent the datagram. The kernel already knows the number - it is
// what made the write fail - but only tells it to a connected socket, hence the
// throwaway one below.
type pathMTUPacketConn struct {
	net.PacketConn
	options []dialer.Option

	access sync.Mutex
	cache  map[netip.Addr]pathMTUCacheEntry
}

type pathMTUCacheEntry struct {
	mtu       uint32
	createdAt time.Time
}

func newPathMTUPacketConn(packetConn net.PacketConn, options []dialer.Option) net.PacketConn {
	if !pathMTUSupported {
		return packetConn
	}
	return &pathMTUPacketConn{PacketConn: packetConn, options: options}
}

func (c *pathMTUPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(p, addr)
	if err == nil || !errors.Is(err, syscall.EMSGSIZE) {
		return n, err
	}
	destination, ok := addrPortFromNetAddr(addr)
	if !ok {
		return n, err
	}
	mtu := c.pathMTU(destination)
	if mtu == 0 {
		return n, err
	}
	return n, &C.PacketTooBigError{MTU: mtu, Err: err}
}

func (c *pathMTUPacketConn) pathMTU(destination netip.AddrPort) uint32 {
	now := time.Now()
	c.access.Lock()
	if entry, loaded := c.cache[destination.Addr()]; loaded && now.Sub(entry.createdAt) < pathMTUCacheTTL {
		c.access.Unlock()
		return entry.mtu
	}
	c.access.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	mtu := queryPathMTU(ctx, destination, c.options)

	c.access.Lock()
	if c.cache == nil {
		c.cache = make(map[netip.Addr]pathMTUCacheEntry)
	}
	if len(c.cache) > 64 {
		for oldDestination := range c.cache {
			delete(c.cache, oldDestination)
			break
		}
	}
	c.cache[destination.Addr()] = pathMTUCacheEntry{mtu: mtu, createdAt: now}
	c.access.Unlock()
	return mtu
}

func (c *pathMTUPacketConn) Upstream() any {
	return c.PacketConn
}

func addrPortFromNetAddr(addr net.Addr) (netip.AddrPort, bool) {
	if udpAddr, isUDPAddr := addr.(*net.UDPAddr); isUDPAddr {
		addrPort := udpAddr.AddrPort()
		return addrPort, addrPort.IsValid()
	}
	addrPort, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return netip.AddrPort{}, false
	}
	return addrPort, true
}
