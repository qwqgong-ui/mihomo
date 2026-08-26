package udp

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/transport/hysteria/obfs"
	"github.com/metacubex/mihomo/transport/hysteria/utils"

	"github.com/metacubex/randv2"
)

const (
	packetQueueSize = 1024
	oobBufferSize   = 256
)

// ObfsUDPHopClientPacketConn is the UDP port-hopping packet connection for client side.
// It hops to a different local & server port every once in a while.
type ObfsUDPHopClientPacketConn struct {
	serverAddr  net.Addr // Combined udpHopAddr
	serverAddrs []net.Addr
	hopInterval time.Duration

	obfs obfs.Obfuscator

	connMutex   sync.RWMutex
	currentConn net.PacketConn
	addrIndex   int
	remoteAddr  *net.UDPAddr

	readBufferSize  int
	writeBufferSize int

	recvQueue chan *udpPacket
	closeChan chan struct{}
	closed    bool

	bufPool sync.Pool
	oobPool sync.Pool
}

type udpHopAddr string

func (a *udpHopAddr) Network() string {
	return "udp-hop"
}

func (a *udpHopAddr) String() string {
	return string(*a)
}

type udpPacket struct {
	buf   []byte
	n     int
	oob   []byte
	oobn  int
	flags int
	addr  net.Addr
}

func NewObfsUDPHopClientPacketConn(server string, serverPorts string, hopInterval time.Duration, obfs obfs.Obfuscator, dialer utils.PacketDialer) (net.PacketConn, error) {
	ports, err := parsePorts(serverPorts)
	if err != nil {
		return nil, err
	}
	// Resolve the server IP address, then attach the ports to UDP addresses
	rAddr, err := dialer.RemoteAddr(server)
	if err != nil {
		return nil, err
	}
	remoteAddr, ok := rAddr.(*net.UDPAddr)
	if !ok {
		remoteAddr, err = net.ResolveUDPAddr("udp", rAddr.String())
		if err != nil {
			return nil, err
		}
	}
	ip, _, err := net.SplitHostPort(rAddr.String())
	if err != nil {
		return nil, err
	}
	serverAddrs := make([]net.Addr, len(ports))
	for i, port := range ports {
		serverAddrs[i] = &net.UDPAddr{
			IP:   net.ParseIP(ip),
			Port: int(port),
		}
	}
	hopAddr := udpHopAddr(server)
	conn := &ObfsUDPHopClientPacketConn{
		serverAddr:  &hopAddr,
		serverAddrs: serverAddrs,
		hopInterval: hopInterval,
		obfs:        obfs,
		addrIndex:   randv2.IntN(len(serverAddrs)),
		remoteAddr:  remoteAddr,
		recvQueue:   make(chan *udpPacket, packetQueueSize),
		closeChan:   make(chan struct{}),
		bufPool: sync.Pool{
			New: func() interface{} {
				return make([]byte, udpBufferSize)
			},
		},
		oobPool: sync.Pool{
			New: func() interface{} {
				return make([]byte, oobBufferSize)
			},
		},
	}
	curConn, err := dialer.ListenPacket(rAddr)
	if err != nil {
		return nil, err
	}
	if obfs != nil {
		conn.currentConn = NewObfsUDPConn(curConn, obfs)
	} else {
		conn.currentConn = curConn
	}
	go conn.recvRoutine(conn.currentConn)
	go conn.hopRoutine()
	if _, ok := conn.currentConn.(oobCapablePacketConn); ok {
		return &ObfsUDPHopClientPacketConnWithOOB{conn}, nil
	}
	return conn, nil
}

func (c *ObfsUDPHopClientPacketConn) recvRoutine(conn net.PacketConn) {
	if oobConn, ok := conn.(oobCapablePacketConn); ok {
		c.recvRoutineOOB(oobConn)
		return
	}
	for {
		buf := c.bufPool.Get().([]byte)
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		select {
		case c.recvQueue <- &udpPacket{buf: buf, n: n, addr: addr}:
		default:
			// Drop the packet if the queue is full
			c.bufPool.Put(buf)
		}
	}
}

func (c *ObfsUDPHopClientPacketConn) recvRoutineOOB(conn oobCapablePacketConn) {
	for {
		buf := c.bufPool.Get().([]byte)
		oob := c.oobPool.Get().([]byte)
		n, oobn, flags, addr, err := conn.ReadMsgUDP(buf, oob)
		if err != nil {
			c.bufPool.Put(buf)
			c.oobPool.Put(oob)
			return
		}
		select {
		case c.recvQueue <- &udpPacket{buf: buf, n: n, oob: oob, oobn: oobn, flags: flags, addr: addr}:
		default:
			c.bufPool.Put(buf)
			c.oobPool.Put(oob)
		}
	}
}

func (c *ObfsUDPHopClientPacketConn) hopRoutine() {
	ticker := time.NewTicker(c.hopInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.hop()
		case <-c.closeChan:
			return
		}
	}
}

func (c *ObfsUDPHopClientPacketConn) hop() {
	c.connMutex.Lock()
	defer c.connMutex.Unlock()
	if c.closed {
		return
	}
	// Keep the local UDP socket so the OOB socket options installed by
	// quic-go remain active. Port hopping only needs to change the remote
	// destination port.
	c.addrIndex = randv2.IntN(len(c.serverAddrs))
}

func (c *ObfsUDPHopClientPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	p, err := c.readPacket()
	if err != nil {
		return 0, nil, err
	}
	n := copy(b, p.buf[:p.n])
	c.releasePacket(p)
	return n, c.serverAddr, nil
}

func (c *ObfsUDPHopClientPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	c.connMutex.RLock()
	defer c.connMutex.RUnlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	/*
		// Check if the address is the server address
		if addr.String() != c.serverAddr.String() {
			return 0, net.ErrWriteToConnected
		}
	*/
	// Skip the check for now, always write to the server
	return c.currentConn.WriteTo(b, c.serverAddrs[c.addrIndex])
}

func (c *ObfsUDPHopClientPacketConn) Close() error {
	c.connMutex.Lock()
	defer c.connMutex.Unlock()
	if c.closed {
		return nil
	}
	// Close currentConn
	// Close closeChan to unblock ReadFrom & hopRoutine
	// Set closed flag to true to prevent double close
	err := c.currentConn.Close()
	close(c.closeChan)
	c.closed = true
	c.serverAddrs = nil // For GC
	return err
}

func (c *ObfsUDPHopClientPacketConn) LocalAddr() net.Addr {
	c.connMutex.RLock()
	defer c.connMutex.RUnlock()
	return c.currentConn.LocalAddr()
}

func (c *ObfsUDPHopClientPacketConn) SetReadDeadline(t time.Time) error {
	// Not supported
	return nil
}

func (c *ObfsUDPHopClientPacketConn) SetWriteDeadline(t time.Time) error {
	// Not supported
	return nil
}

func (c *ObfsUDPHopClientPacketConn) SetDeadline(t time.Time) error {
	err := c.SetReadDeadline(t)
	if err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *ObfsUDPHopClientPacketConn) SetReadBuffer(bytes int) error {
	c.connMutex.Lock()
	defer c.connMutex.Unlock()
	c.readBufferSize = bytes
	return trySetPacketConnReadBuffer(c.currentConn, bytes)
}

func (c *ObfsUDPHopClientPacketConn) SetWriteBuffer(bytes int) error {
	c.connMutex.Lock()
	defer c.connMutex.Unlock()
	c.writeBufferSize = bytes
	return trySetPacketConnWriteBuffer(c.currentConn, bytes)
}

func trySetPacketConnReadBuffer(pc net.PacketConn, bytes int) error {
	sc, ok := pc.(interface {
		SetReadBuffer(bytes int) error
	})
	if ok {
		return sc.SetReadBuffer(bytes)
	}
	return nil
}

func trySetPacketConnWriteBuffer(pc net.PacketConn, bytes int) error {
	sc, ok := pc.(interface {
		SetWriteBuffer(bytes int) error
	})
	if ok {
		return sc.SetWriteBuffer(bytes)
	}
	return nil
}

type ObfsUDPHopClientPacketConnWithOOB struct {
	*ObfsUDPHopClientPacketConn
}

func (c *ObfsUDPHopClientPacketConnWithOOB) SyscallConn() (syscall.RawConn, error) {
	c.connMutex.RLock()
	defer c.connMutex.RUnlock()
	sc, ok := c.currentConn.(syscall.Conn)
	if !ok {
		return nil, errors.New("not supported")
	}
	return sc.SyscallConn()
}

func (c *ObfsUDPHopClientPacketConnWithOOB) ReadMsgUDP(b, oob []byte) (n, oobn, flags int, addr *net.UDPAddr, err error) {
	p, err := c.readPacket()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	n = copy(b, p.buf[:p.n])
	oobn = copy(oob, p.oob[:p.oobn])
	flags = p.flags
	c.releasePacket(p)
	return n, oobn, flags, c.remoteAddr, nil
}

func (c *ObfsUDPHopClientPacketConnWithOOB) WriteMsgUDP(b, oob []byte, _ *net.UDPAddr) (n, oobn int, err error) {
	c.connMutex.RLock()
	defer c.connMutex.RUnlock()
	if c.closed {
		return 0, 0, net.ErrClosed
	}
	oobConn, ok := c.currentConn.(oobCapablePacketConn)
	if !ok {
		return 0, 0, errors.New("OOB packet I/O is not supported")
	}
	addr, ok := c.serverAddrs[c.addrIndex].(*net.UDPAddr)
	if !ok {
		return 0, 0, errors.New("server address is not UDP")
	}
	return oobConn.WriteMsgUDP(b, oob, addr)
}

func (c *ObfsUDPHopClientPacketConn) readPacket() (*udpPacket, error) {
	select {
	case p := <-c.recvQueue:
		return p, nil
	case <-c.closeChan:
		return nil, net.ErrClosed
	}
}

func (c *ObfsUDPHopClientPacketConn) releasePacket(p *udpPacket) {
	c.bufPool.Put(p.buf)
	if p.oob != nil {
		c.oobPool.Put(p.oob)
	}
}

var _ oobCapablePacketConn = (*ObfsUDPHopClientPacketConnWithOOB)(nil)

// parsePorts parses the multi-port server address and returns the host and ports.
// Supports both comma-separated single ports and dash-separated port ranges.
// Format: "host:port1,port2-port3,port4"
func parsePorts(serverPorts string) (ports []uint16, err error) {
	portStrs := strings.Split(serverPorts, ",")
	for _, portStr := range portStrs {
		if strings.Contains(portStr, "-") {
			// Port range
			portRange := strings.Split(portStr, "-")
			if len(portRange) != 2 {
				return nil, net.InvalidAddrError("invalid port range")
			}
			start, err := strconv.ParseUint(portRange[0], 10, 16)
			if err != nil {
				return nil, net.InvalidAddrError("invalid port range")
			}
			end, err := strconv.ParseUint(portRange[1], 10, 16)
			if err != nil {
				return nil, net.InvalidAddrError("invalid port range")
			}
			if start > end {
				start, end = end, start
			}
			for i := start; i <= end; i++ {
				ports = append(ports, uint16(i))
			}
		} else {
			// Single port
			port, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return nil, net.InvalidAddrError("invalid port")
			}
			ports = append(ports, uint16(port))
		}
	}
	if len(ports) == 0 {
		return nil, net.InvalidAddrError("invalid port")
	}
	return ports, nil
}
