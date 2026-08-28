package dialer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	R "github.com/metacubex/mihomo/component/resolver"
)

type progressiveCandidateEvent struct {
	R.IPCandidateBatch
	ipv6 bool
	done bool
}

type progressiveConnectResult struct {
	dialResult
	ipv6 bool
	rtt  time.Duration
}

func canUseProgressiveDirect(host string) bool {
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	_, static := R.DefaultHosts.Search(host, false)
	return !static
}

func directProgressiveDialContext(ctx context.Context, network, address string, opt option, progressive R.ProgressiveResolver) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	scope := directNetworkScope(opt)
	cacheKey, cacheable := tcpConcurrentCacheScopedKey(host, port, network, scope)
	if !cacheable {
		cacheKey = ""
	}

	workCtx, cancelWork := context.WithTimeout(context.Background(), R.DefaultDNSTimeout+DefaultTCPTimeout)
	detached := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			cancelWork()
		case <-detached:
		case <-finished:
		}
	}()

	resultCh := make(chan dialResult, 1)
	go runProgressiveDirectRace(workCtx, cancelWork, finished, detached, resultCh, network, host, port, scope, opt, progressive, cacheKey)
	select {
	case result := <-resultCh:
		return result.Conn, result.error
	case <-ctx.Done():
		select {
		case result := <-resultCh:
			return result.Conn, result.error
		default:
			return nil, ctx.Err()
		}
	}
}

func runProgressiveDirectRace(
	ctx context.Context,
	cancel context.CancelFunc,
	finished chan struct{},
	detached chan struct{},
	resultCh chan<- dialResult,
	network, host, port, scope string,
	opt option,
	progressive R.ProgressiveResolver,
	cacheKey string,
) {
	defer cancel()
	defer close(finished)

	events := make(chan progressiveCandidateEvent, 8)
	feed := func(ipv6 bool) {
		for batch := range progressive.LookupIPCandidates(ctx, host, ipv6, scope) {
			select {
			case events <- progressiveCandidateEvent{IPCandidateBatch: batch, ipv6: ipv6}:
			case <-ctx.Done():
				return
			}
		}
		select {
		case events <- progressiveCandidateEvent{ipv6: ipv6, done: true}:
		case <-ctx.Done():
		}
	}

	families := 0
	var startedFamily [2]bool
	if network != "tcp6" {
		families++
		startedFamily[0] = true
		go feed(false)
	}
	if network != "tcp4" && !R.DisableIPv6.Load() {
		families++
		startedFamily[1] = true
		go feed(true)
	}
	if families == 0 {
		resultCh <- dialResult{error: ErrorNoIpAddress}
		return
	}
	connects := make(chan progressiveConnectResult, 32)
	seen := make(map[netip.Addr]struct{})
	pending := 0
	var pendingFamily [2]int
	doneFamilies := 0
	var doneFamily [2]bool
	var errs []error
	var bestRTT time.Duration
	var delivered bool
	var detachOnce sync.Once
	var heldFallback net.Conn
	preferredIPv6 := opt.prefer == 6
	preferenceEnabled := opt.prefer == 4 || opt.prefer == 6
	var preferenceTimer *time.Timer
	var preferenceTimeout <-chan time.Time

	deliver := func(conn net.Conn, ip netip.Addr) {
		if delivered {
			if conn != nil {
				_ = conn.Close()
			}
			return
		}
		delivered = true
		detachOnce.Do(func() { close(detached) })
		resultCh <- dialResult{ip: ip, Conn: conn}
	}
	promote := func(result progressiveConnectResult) {
		if result.rtt <= 0 || bestRTT > 0 && result.rtt >= bestRTT {
			return
		}
		first := bestRTT == 0
		bestRTT = result.rtt
		if cacheKey != "" {
			if first {
				tcpConcurrentCache.SetWithRTT(cacheKey, result.ip, result.rtt)
			} else {
				tcpConcurrentCache.SetIfFaster(cacheKey, result.ip, result.rtt)
			}
		}
		progressive.PromoteIP(host, result.ipv6, scope, result.ip)
	}
	start := func(ip netip.Addr, ipv6 bool) {
		ip = ip.Unmap()
		if !ip.IsValid() {
			return
		}
		if _, loaded := seen[ip]; loaded {
			return
		}
		seen[ip] = struct{}{}
		pending++
		family := 0
		if ipv6 {
			family = 1
		}
		pendingFamily[family]++
		go func() {
			connectOpt := opt
			connectOpt.tfo = false
			started := time.Now()
			conn, err := dialContext(ctx, network, ip, port, connectOpt)
			result := progressiveConnectResult{
				dialResult: dialResult{ip: ip, Conn: conn, error: err},
				ipv6:       ipv6,
				rtt:        time.Since(started),
			}
			select {
			case connects <- result:
			case <-ctx.Done():
				if conn != nil {
					_ = conn.Close()
				}
			}
		}()
	}
	preferredDone := func() bool {
		if !preferenceEnabled {
			return true
		}
		family := 0
		if preferredIPv6 {
			family = 1
		}
		return !startedFamily[family] || doneFamily[family] && pendingFamily[family] == 0
	}

	for {
		if doneFamilies == families && pending == 0 {
			if !delivered {
				if heldFallback != nil {
					deliver(heldFallback, netip.Addr{})
					heldFallback = nil
				} else {
					err := errors.Join(errs...)
					if err == nil {
						err = ErrorNoIpAddress
					}
					resultCh <- dialResult{error: err}
				}
			}
			return
		}
		select {
		case <-ctx.Done():
			if heldFallback != nil {
				_ = heldFallback.Close()
			}
			if !delivered {
				resultCh <- dialResult{error: ctx.Err()}
			}
			return
		case event := <-events:
			if event.done {
				doneFamilies++
				if event.ipv6 {
					doneFamily[1] = true
				} else {
					doneFamily[0] = true
				}
				if heldFallback != nil && preferredDone() && !delivered {
					deliver(heldFallback, netip.Addr{})
					heldFallback = nil
				}
				continue
			}
			if event.Err != nil {
				errs = append(errs, event.Err)
				continue
			}
			for _, ip := range event.IPs {
				start(ip, event.ipv6)
			}
		case result := <-connects:
			pending--
			if result.ipv6 {
				pendingFamily[1]--
			} else {
				pendingFamily[0]--
			}
			if result.error != nil {
				errs = append(errs, fmt.Errorf("connect %s failed: %w", result.ip, result.error))
				if heldFallback != nil && preferredDone() && !delivered {
					deliver(heldFallback, netip.Addr{})
					heldFallback = nil
				}
				continue
			}
			promote(result)
			if delivered {
				_ = result.Conn.Close()
				continue
			}
			isPreferred := !preferenceEnabled || result.ipv6 == preferredIPv6
			if isPreferred {
				if heldFallback != nil {
					_ = heldFallback.Close()
					heldFallback = nil
				}
				deliver(result.Conn, result.ip)
				continue
			}
			if heldFallback == nil {
				heldFallback = result.Conn
				preferenceTimer = time.NewTimer(dualStackFallbackTimeout)
				preferenceTimeout = preferenceTimer.C
			} else {
				_ = result.Conn.Close()
			}
			if preferredDone() {
				deliver(heldFallback, result.ip)
				heldFallback = nil
			}
		case <-preferenceTimeout:
			preferenceTimeout = nil
			if heldFallback != nil && !delivered {
				deliver(heldFallback, netip.Addr{})
				heldFallback = nil
			}
		}
	}
}

func isTCPNetwork(network string) bool {
	return strings.HasPrefix(network, "tcp")
}
