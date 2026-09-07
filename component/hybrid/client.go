package hybrid

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

const probeInterval = 250 * time.Millisecond
const probeTimeout = 3 * time.Second
const rawSilence = 15 * time.Second

type ClientOptions struct {
	Dial     func(context.Context) (net.Conn, error)
	Raw      func(context.Context, netip.AddrPort) (net.PacketConn, error)
	Fallback func(context.Context) (net.PacketConn, error)
}
type PacketConn struct {
	opts            ClientOptions
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	flows           map[string]*clientFlow
	fallback        net.PacketConn
	reads           chan result
	closed          chan struct{}
	once            sync.Once
	deadline        time.Time
	deadlineChanged chan struct{}
	writeDeadline   time.Time
}
type clientFlow struct {
	owner                        *PacketConn
	ready                        chan struct{}
	err                          error
	stream                       net.Conn
	raw                          net.PacketConn
	relay                        netip.AddrPort
	target                       netip.AddrPort
	writeMu                      sync.Mutex
	mu                           sync.Mutex
	disabled, active             bool
	probeEnd, nextProbe, lastRaw time.Time
	done                         chan struct{}
}
type result struct {
	p   []byte
	a   net.Addr
	err error
}

func NewPacketConn(opts ClientOptions) *PacketConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &PacketConn{opts: opts, ctx: ctx, cancel: cancel, flows: make(map[string]*clientFlow), reads: make(chan result, 64), closed: make(chan struct{}), deadlineChanged: make(chan struct{})}
}
func (c *PacketConn) WriteTo(p []byte, a net.Addr) (int, error) {
	if len(p) == 0 || len(p) > MaxPacket {
		return 0, errors.New("hybrid: invalid datagram size")
	}
	if a == nil {
		return 0, errors.New("hybrid: missing destination")
	}
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return 0, err
	}
	if IsReserved(host) {
		return 0, errors.New("hybrid: reserved destination")
	}
	if port != "443" {
		return c.writeFallback(p, a)
	}
	if ip, err := netip.ParseAddr(host); err == nil && !Public(ip) {
		return c.writeFallback(p, a)
	}
	key := a.String()
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	f := c.flows[key]
	if f == nil {
		if !Initial(p) {
			c.mu.Unlock()
			return c.writeFallback(p, a)
		}
		f = &clientFlow{owner: c, ready: make(chan struct{}), done: make(chan struct{})}
		c.flows[key] = f
		go f.open(key)
	}
	c.mu.Unlock()
	select {
	case <-f.ready:
	case <-c.closed:
		return 0, net.ErrClosed
	}
	if f.err != nil {
		return c.writeFallback(p, a)
	}
	return f.write(p)
}
func (f *clientFlow) open(address string) {
	c := f.owner
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	stream, err := c.opts.Dial(ctx)
	if err != nil {
		f.err = err
		close(f.ready)
		return
	}
	stop := context.AfterFunc(ctx, func() { stream.Close() })
	fail := func(err error) { stop(); stream.Close(); f.err = err; close(f.ready) }
	stream.SetDeadline(time.Now().Add(10 * time.Second))
	if err = WriteRequest(stream, Request{Target: address}); err != nil {
		fail(err)
		return
	}
	var status [1]byte
	if _, err = io.ReadFull(stream, status[:]); err != nil {
		fail(err)
		return
	}
	if status[0] != 0 {
		fail(errors.New("hybrid: flow rejected"))
		return
	}
	target, err := ReadAddress(stream)
	if err != nil {
		fail(err)
		return
	}
	relay, err := ReadAddress(stream)
	if err != nil {
		fail(err)
		return
	}
	f.target, err = netip.ParseAddrPort(target)
	if err != nil || !Public(f.target.Addr()) || f.target.Port() != 443 {
		fail(errors.New("hybrid: invalid resolved target"))
		return
	}
	f.relay, err = netip.ParseAddrPort(relay)
	if err != nil || f.relay.Port() != 443 {
		fail(errors.New("hybrid: invalid relay"))
		return
	}
	if Public(f.relay.Addr()) {
		f.raw, _ = c.opts.Raw(ctx, f.relay)
	}
	f.disabled = f.raw == nil
	// Stop the setup timer before handing the stream to its lifetime owner.
	if !stop() || ctx.Err() != nil {
		if f.raw != nil {
			f.raw.Close()
		}
		fail(context.DeadlineExceeded)
		return
	}
	stream.SetDeadline(time.Time{})
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		if f.raw != nil {
			f.raw.Close()
		}
		fail(net.ErrClosed)
		return
	}
	f.stream = stream
	stream.SetWriteDeadline(c.writeDeadline)
	c.mu.Unlock()
	close(f.ready)
	go f.readStream()
	go f.watch()
	if f.raw != nil {
		go f.readRaw()
	} else {
		f.disable(true)
	}
}
func (f *clientFlow) disable(notify bool) error {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	f.mu.Lock()
	f.disabled = true
	f.active = false
	f.mu.Unlock()
	// Sending an idempotent zero frame also covers raw setup failure.
	if notify {
		return WriteFrame(f.stream, nil)
	}
	return nil
}
func (f *clientFlow) write(p []byte) (int, error) {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	now := time.Now()
	f.mu.Lock()
	disable := false
	raw, probe := false, false
	if !f.disabled && Short(p) {
		if f.active {
			if !f.lastRaw.IsZero() && now.Sub(f.lastRaw) >= rawSilence {
				disable = true
			} else {
				raw = true
			}
		} else {
			if f.probeEnd.IsZero() {
				f.probeEnd = now.Add(probeTimeout)
			}
			if !now.Before(f.probeEnd) {
				disable = true
			} else if !now.Before(f.nextProbe) {
				probe = true
				f.nextProbe = now.Add(probeInterval)
			}
		}
	}
	if disable {
		f.disabled = true
		f.active = false
	}
	f.mu.Unlock()
	if disable {
		if err := WriteFrame(f.stream, nil); err != nil {
			return 0, err
		}
	}
	if raw {
		if _, err := f.raw.WriteTo(p, net.UDPAddrFromAddrPort(f.relay)); err == nil {
			return len(p), nil
		}
		f.mu.Lock()
		f.disabled = true
		f.active = false
		f.mu.Unlock()
		if err := WriteFrame(f.stream, nil); err != nil {
			return 0, err
		}
	}
	if err := WriteFrame(f.stream, p); err != nil {
		return 0, err
	}
	if probe {
		if _, err := f.raw.WriteTo(p, net.UDPAddrFromAddrPort(f.relay)); err != nil {
			f.mu.Lock()
			f.disabled = true
			f.mu.Unlock()
			if err = WriteFrame(f.stream, nil); err != nil {
				return 0, err
			}
		}
	}
	return len(p), nil
}
func (f *clientFlow) readStream() {
	defer func() {
		close(f.done)
		f.stream.Close()
		if f.raw != nil {
			f.raw.Close()
		}
	}()
	for {
		p, err := ReadFrame(f.stream)
		if err != nil {
			f.owner.deliver(result{err: err})
			return
		}
		if p == nil {
			continue
		}
		if len(p) == 0 {
			f.disable(false)
			continue
		}
		f.owner.deliver(result{p: p, a: net.UDPAddrFromAddrPort(f.target)})
	}
}
func (f *clientFlow) readRaw() {
	b := make([]byte, 65536)
	for {
		n, a, err := f.raw.ReadFrom(b)
		if err != nil {
			if f.owner.ctx.Err() == nil {
				if err = f.disable(true); err != nil {
					f.owner.deliver(result{err: err})
				}
			}
			return
		}
		if a.String() != f.relay.String() || !Short(b[:n]) {
			continue
		}
		f.mu.Lock()
		if f.disabled {
			f.mu.Unlock()
			continue
		}
		f.active = true
		f.lastRaw = time.Now()
		f.mu.Unlock()
		f.owner.deliver(result{p: append([]byte(nil), b[:n]...), a: net.UDPAddrFromAddrPort(f.target)})
	}
}
func (c *PacketConn) writeFallback(p []byte, a net.Addr) (int, error) {
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	pc := c.fallback
	c.mu.Unlock()
	if pc == nil {
		fresh, err := c.opts.Fallback(c.ctx)
		if err != nil {
			return 0, err
		}
		c.mu.Lock()
		if c.ctx.Err() != nil {
			c.mu.Unlock()
			fresh.Close()
			return 0, net.ErrClosed
		}
		if c.fallback == nil {
			c.fallback = fresh
			fresh.SetWriteDeadline(c.writeDeadline)
			go c.readFallback(fresh)
		} else {
			fresh.Close()
		}
		pc = c.fallback
		c.mu.Unlock()
	}
	return pc.WriteTo(p, a)
}
func (c *PacketConn) readFallback(pc net.PacketConn) {
	b := make([]byte, 65536)
	for {
		n, a, e := pc.ReadFrom(b)
		if e != nil {
			c.deliver(result{err: e})
			return
		}
		c.deliver(result{p: append([]byte(nil), b[:n]...), a: a})
	}
}
func (c *PacketConn) deliver(r result) {
	select {
	case c.reads <- r:
	case <-c.closed:
	}
}
func (c *PacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		c.mu.Lock()
		deadline, changed := c.deadline, c.deadlineChanged
		c.mu.Unlock()
		var timer *time.Timer
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			if !time.Now().Before(deadline) {
				return 0, nil, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(time.Until(deadline))
			timeout = timer.C
		}
		var r result
		select {
		case r = <-c.reads:
		case <-c.closed:
			r.err = net.ErrClosed
		case <-timeout:
			r.err = os.ErrDeadlineExceeded
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
			continue
		}
		if timer != nil {
			timer.Stop()
		}
		return copy(b, r.p), r.a, r.err
	}
}
func (c *PacketConn) Close() error {
	c.once.Do(func() {
		c.cancel()
		close(c.closed)
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, f := range c.flows {
			if f.stream != nil {
				f.stream.Close()
				if f.raw != nil {
					f.raw.Close()
				}
			}
		}
		if c.fallback != nil {
			c.fallback.Close()
		}
	})
	return nil
}
func (c *PacketConn) LocalAddr() net.Addr { return &net.UDPAddr{} }
func (c *PacketConn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}
func (c *PacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	close(c.deadlineChanged)
	c.deadlineChanged = make(chan struct{})
	return nil
}
func (c *PacketConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeDeadline = t
	for _, f := range c.flows {
		if f.stream != nil {
			f.stream.SetWriteDeadline(t)
			if f.raw != nil {
				f.raw.SetWriteDeadline(t)
			}
		}
	}
	if c.fallback != nil {
		c.fallback.SetWriteDeadline(t)
	}
	return nil
}

func (f *clientFlow) watch() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastKeep := time.Now()
	for {
		select {
		case <-f.owner.closed:
			return
		case <-f.done:
			return
		case now := <-ticker.C:
			f.writeMu.Lock()
			f.mu.Lock()
			expired := !f.disabled && ((f.active && now.Sub(f.lastRaw) >= rawSilence) || (!f.active && !f.probeEnd.IsZero() && !now.Before(f.probeEnd)))
			if expired {
				f.disabled = true
				f.active = false
			}
			f.mu.Unlock()
			var err error
			if expired {
				err = WriteFrame(f.stream, nil)
			}
			if err == nil && now.Sub(lastKeep) >= 30*time.Second {
				err = WriteKeepAlive(f.stream)
				lastKeep = now
			}
			f.writeMu.Unlock()
			if err != nil {
				f.stream.Close()
				return
			}
		}
	}
}
