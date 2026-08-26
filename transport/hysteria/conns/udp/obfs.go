package udp

import (
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/transport/hysteria/obfs"
)

const udpBufferSize = 65535

type ObfsUDPConn struct {
	orig       net.PacketConn
	obfs       obfs.Obfuscator
	readBuf    []byte
	readMutex  sync.Mutex
	writeBuf   []byte
	writeMutex sync.Mutex
}

type oobCapablePacketConn interface {
	net.PacketConn
	SyscallConn() (syscall.RawConn, error)
	SetReadBuffer(int) error
	ReadMsgUDP(b, oob []byte) (n, oobn, flags int, addr *net.UDPAddr, err error)
	WriteMsgUDP(b, oob []byte, addr *net.UDPAddr) (n, oobn int, err error)
}

func NewObfsUDPConn(orig net.PacketConn, obfs obfs.Obfuscator) net.PacketConn {
	conn := &ObfsUDPConn{
		orig:     orig,
		obfs:     obfs,
		readBuf:  make([]byte, udpBufferSize),
		writeBuf: make([]byte, udpBufferSize),
	}
	if oobConn, ok := orig.(oobCapablePacketConn); ok {
		return &ObfsUDPConnWithOOB{ObfsUDPConn: conn, orig: oobConn}
	}
	return conn
}

func (c *ObfsUDPConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		c.readMutex.Lock()
		n, addr, err := c.orig.ReadFrom(c.readBuf)
		if n <= 0 {
			c.readMutex.Unlock()
			return 0, addr, err
		}
		newN := c.obfs.Deobfuscate(c.readBuf[:n], p)
		c.readMutex.Unlock()
		if newN > 0 {
			// Valid packet
			return newN, addr, err
		} else if err != nil {
			// Not valid and orig.ReadFrom had some error
			return 0, addr, err
		}
	}
}

func (c *ObfsUDPConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	c.writeMutex.Lock()
	bn := c.obfs.Obfuscate(p, c.writeBuf)
	_, err = c.orig.WriteTo(c.writeBuf[:bn], addr)
	c.writeMutex.Unlock()
	if err != nil {
		return 0, err
	} else {
		return len(p), nil
	}
}

func (c *ObfsUDPConn) Close() error {
	return c.orig.Close()
}

func (c *ObfsUDPConn) LocalAddr() net.Addr {
	return c.orig.LocalAddr()
}

func (c *ObfsUDPConn) SetDeadline(t time.Time) error {
	return c.orig.SetDeadline(t)
}

func (c *ObfsUDPConn) SetReadDeadline(t time.Time) error {
	return c.orig.SetReadDeadline(t)
}

func (c *ObfsUDPConn) SetWriteDeadline(t time.Time) error {
	return c.orig.SetWriteDeadline(t)
}

// ObfsUDPConnWithOOB preserves the out-of-band control messages used by
// quic-go for packet info, ECN and UDP segmentation while transforming only
// the UDP payload.
type ObfsUDPConnWithOOB struct {
	*ObfsUDPConn
	orig oobCapablePacketConn
}

func (c *ObfsUDPConnWithOOB) SyscallConn() (syscall.RawConn, error) {
	return c.orig.SyscallConn()
}

func (c *ObfsUDPConnWithOOB) SetReadBuffer(bytes int) error {
	return c.orig.SetReadBuffer(bytes)
}

func (c *ObfsUDPConnWithOOB) ReadMsgUDP(p, oob []byte) (n, oobn, flags int, addr *net.UDPAddr, err error) {
	for {
		c.readMutex.Lock()
		n, oobn, flags, addr, err = c.orig.ReadMsgUDP(c.readBuf, oob)
		if n <= 0 {
			c.readMutex.Unlock()
			return 0, oobn, flags, addr, err
		}
		newN := c.obfs.Deobfuscate(c.readBuf[:n], p)
		c.readMutex.Unlock()
		if newN > 0 {
			return newN, oobn, flags, addr, err
		}
		if err != nil {
			return 0, oobn, flags, addr, err
		}
	}
}

func (c *ObfsUDPConnWithOOB) WriteMsgUDP(p, oob []byte, addr *net.UDPAddr) (n, oobn int, err error) {
	c.writeMutex.Lock()
	bn := c.obfs.Obfuscate(p, c.writeBuf)
	_, oobn, err = c.orig.WriteMsgUDP(c.writeBuf[:bn], oob, addr)
	c.writeMutex.Unlock()
	if err != nil {
		return 0, oobn, err
	}
	return len(p), oobn, nil
}

var _ oobCapablePacketConn = (*ObfsUDPConnWithOOB)(nil)
