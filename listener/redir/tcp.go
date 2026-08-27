package redir

import (
	"context"
	"net"

	"github.com/metacubex/mihomo/adapter/inbound"
	"github.com/metacubex/mihomo/component/keepalive"
	"github.com/metacubex/mihomo/component/mptcp"
	C "github.com/metacubex/mihomo/constant"
)

type Listener struct {
	listener net.Listener
	addr     string
	closed   bool
}

// RawAddress implements C.Listener
func (l *Listener) RawAddress() string {
	return l.addr
}

// Address implements C.Listener
func (l *Listener) Address() string {
	return l.listener.Addr().String()
}

// Close implements C.Listener
func (l *Listener) Close() error {
	l.closed = true
	return l.listener.Close()
}

func New(addr string, tunnel C.Tunnel, additions ...inbound.Addition) (*Listener, error) {
	if len(additions) == 0 {
		additions = []inbound.Addition{
			inbound.WithInName("DEFAULT-REDIR"),
			inbound.WithSpecialRules(""),
		}
	}

	// Golang will then enable mptcp support for listeners by default when the major version of go.mod is 1.24 or higher.
	// getsockopt(SO_ORIGINAL_DST) answers EOPNOTSUPP on an mptcp socket, which would fail every redirected
	// connection, so we force to disable mptcp for redir as tproxy already does.
	lc := net.ListenConfig{}
	mptcp.SetNetListenConfig(&lc, false)
	l, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}

	rl := &Listener{
		listener: l,
		addr:     addr,
	}

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if rl.closed {
					break
				}
				continue
			}
			go handleRedir(c, tunnel, additions...)
		}
	}()

	return rl, nil
}

func handleRedir(conn net.Conn, tunnel C.Tunnel, additions ...inbound.Addition) {
	target, err := parserPacket(conn)
	if err != nil {
		conn.Close()
		return
	}
	keepalive.TCPKeepAlive(conn)
	tunnel.HandleTCPConn(inbound.NewSocket(target, conn, C.REDIR, additions...))
}
