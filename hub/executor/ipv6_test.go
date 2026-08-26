package executor

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/config"
	LC "github.com/metacubex/mihomo/listener/config"
)

func TestDebounceNetworkUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan struct{}, 3)
	callbacks := make(chan struct{}, 2)
	go debounceNetworkUpdates(ctx, updates, 20*time.Millisecond, func() {
		callbacks <- struct{}{}
	})

	updates <- struct{}{}
	time.Sleep(5 * time.Millisecond)
	updates <- struct{}{}
	time.Sleep(5 * time.Millisecond)
	updates <- struct{}{}

	select {
	case <-callbacks:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("debounced network update did not trigger IPv6 detection")
	}
	select {
	case <-callbacks:
		t.Fatal("network update burst triggered more than one IPv6 detection")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRuntimeIPv6StateTransitionsBothDirections(t *testing.T) {
	general := &config.General{IPv6: true}
	state := runtimeIPv6State{configured: true, general: general}

	if changed, active := state.setSystemAvailable(true); !changed || !active || !general.IPv6Active {
		t.Fatalf("enable transition = changed:%v active:%v general:%v", changed, active, general.IPv6Active)
	}
	if changed, active := state.setSystemAvailable(true); changed || !active {
		t.Fatalf("stable available state = changed:%v active:%v", changed, active)
	}
	if changed, active := state.setSystemAvailable(false); !changed || active || general.IPv6Active {
		t.Fatalf("disable transition = changed:%v active:%v general:%v", changed, active, general.IPv6Active)
	}

	state.configured = false
	if changed, active := state.setSystemAvailable(true); changed || active {
		t.Fatalf("disabled configuration became active = changed:%v active:%v", changed, active)
	}
}

func TestTunConfigForIPv6AvailabilityMasksOnlyRuntimeCopy(t *testing.T) {
	v4Prefix := netip.MustParsePrefix("192.0.2.0/24")
	v6Prefix := netip.MustParsePrefix("2001:db8::/32")
	v4Address := netip.MustParseAddr("192.0.2.1")
	v6Address := netip.MustParseAddr("2001:db8::1")
	original := LC.Tun{
		Inet6Address:             []netip.Prefix{v6Prefix},
		RouteAddress:             []netip.Prefix{v4Prefix, v6Prefix},
		RouteExcludeAddress:      []netip.Prefix{v4Prefix, v6Prefix},
		Inet6RouteAddress:        []netip.Prefix{v6Prefix},
		Inet6RouteExcludeAddress: []netip.Prefix{v6Prefix},
		LoopbackAddress:          []netip.Addr{v4Address, v6Address},
	}

	masked := tunConfigForIPv6Availability(original, false)
	if len(masked.Inet6Address) != 0 || len(masked.Inet6RouteAddress) != 0 || len(masked.Inet6RouteExcludeAddress) != 0 {
		t.Fatalf("IPv6-only TUN fields remained active: %+v", masked)
	}
	if len(masked.RouteAddress) != 1 || masked.RouteAddress[0] != v4Prefix || len(masked.RouteExcludeAddress) != 1 || masked.RouteExcludeAddress[0] != v4Prefix {
		t.Fatalf("mixed route fields were not reduced to IPv4: routes=%v excludes=%v", masked.RouteAddress, masked.RouteExcludeAddress)
	}
	if len(masked.LoopbackAddress) != 1 || masked.LoopbackAddress[0] != v4Address {
		t.Fatalf("loopback fields were not reduced to IPv4: %v", masked.LoopbackAddress)
	}
	if len(original.Inet6Address) != 1 || len(original.RouteAddress) != 2 || len(original.LoopbackAddress) != 2 {
		t.Fatalf("configured TUN state was mutated: %+v", original)
	}
}

func TestTunConfigForIPv6AvailabilityKeepsOwnedDescriptor(t *testing.T) {
	v6Prefix := netip.MustParsePrefix("2001:db8::/32")
	original := LC.Tun{FileDescriptor: 7, Inet6Address: []netip.Prefix{v6Prefix}}
	masked := tunConfigForIPv6Availability(original, false)
	if len(masked.Inet6Address) != 1 || masked.Inet6Address[0] != v6Prefix {
		t.Fatalf("externally owned TUN descriptor was masked: %+v", masked)
	}
}

func TestPrepareRuntimeIPv6UsesSharedDetection(t *testing.T) {
	originalCheck := checkSystemIPv6
	originalController := runtimeIPv6Controller
	t.Cleanup(func() {
		checkSystemIPv6 = originalCheck
		stopRuntimeIPv6MonitorLocked()
		runtimeIPv6Controller = originalController
	})

	calls := 0
	checkSystemIPv6 = func() bool {
		calls++
		return true
	}

	cfg := &config.Config{General: &config.General{IPv6: true}, DNS: &config.DNS{}}
	prepareRuntimeIPv6(cfg)

	if calls != 1 {
		t.Fatalf("checkSystemIPv6 was called %d times, want 1", calls)
	}
	if !cfg.General.IPv6Active {
		t.Fatal("IPv6Active was not activated when the system reported IPv6 available")
	}
	if !configuredIPv6.Load() {
		t.Fatal("configuredIPv6 did not mirror the configured intent")
	}
}

// TestPrepareRuntimeIPv6SyncsResolverWithoutWaitingForMonitor guards against a
// regression where resolver.DisableIPv6 only ever got synced to the freshly
// computed availability by applyRuntimeIPv6AvailabilityLocked, which fires
// exclusively from a later network-change event. temporaryUpdateGeneral's
// write to resolver.DisableIPv6 during config parsing is rolled back before
// ApplyConfig runs, so on a fresh start/reload with no subsequent
// network-change event, resolver.DisableIPv6 was staying at its zero-value
// true (disabled) forever even though IPv6 was actually available and
// DNS/TUN were already being wired up as active - breaking DIRECT resolution
// via the resulting active/DisableIPv6 mismatch.
func TestPrepareRuntimeIPv6SyncsResolverWithoutWaitingForMonitor(t *testing.T) {
	originalCheck := checkSystemIPv6
	originalController := runtimeIPv6Controller
	originalDisableIPv6 := resolver.DisableIPv6.Load()
	t.Cleanup(func() {
		checkSystemIPv6 = originalCheck
		stopRuntimeIPv6MonitorLocked()
		runtimeIPv6Controller = originalController
		resolver.DisableIPv6.Store(originalDisableIPv6)
	})

	checkSystemIPv6 = func() bool { return true }
	resolver.DisableIPv6.Store(true)

	cfg := &config.Config{General: &config.General{IPv6: true}, DNS: &config.DNS{}}
	prepareRuntimeIPv6(cfg)

	if resolver.DisableIPv6.Load() {
		t.Fatal("resolver.DisableIPv6 stayed true after prepareRuntimeIPv6 found IPv6 available; " +
			"it must not require a later network-change event to sync")
	}

	checkSystemIPv6 = func() bool { return false }
	resolver.DisableIPv6.Store(false)

	cfg = &config.Config{General: &config.General{IPv6: true}, DNS: &config.DNS{}}
	prepareRuntimeIPv6(cfg)

	if !resolver.DisableIPv6.Load() {
		t.Fatal("resolver.DisableIPv6 stayed false after prepareRuntimeIPv6 found IPv6 unavailable")
	}
}
