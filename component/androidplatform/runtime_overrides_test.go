package androidplatform

import (
	"net/netip"
	"testing"

	"github.com/metacubex/mihomo/component/process"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	LC "github.com/metacubex/mihomo/listener/config"
)

func TestApplyRuntimeOverridesDisablesIPv6(t *testing.T) {
	cfg := &config.Config{
		General: &config.General{
			IPv6:            true,
			FindProcessMode: process.FindProcessAlways,
			Inbound: config.Inbound{Tun: LC.Tun{
				Enable:                      true,
				Inet4Address:                []netip.Prefix{netip.MustParsePrefix("198.18.0.1/30")},
				Inet6Address:                []netip.Prefix{netip.MustParsePrefix("fdfe:dcba:9876::1/126")},
				RouteAddress:                []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")},
				RouteExcludeAddress:         []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("fe80::/10")},
				Inet6RouteAddress:           []netip.Prefix{netip.MustParsePrefix("::/0")},
				Inet6RouteExcludeAddress:    []netip.Prefix{netip.MustParsePrefix("fe80::/10")},
				LoopbackAddress:             []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")},
			}},
		},
		DNS: &config.DNS{IPv6: true},
	}

	err := applyRuntimeOverridesForOptions(cfg, RuntimeOverrides{
		ProcessMatching: false,
		IPv6Enabled:     false,
	})
	if err != nil {
		t.Fatalf("applyRuntimeOverridesForOptions: %v", err)
	}
	if cfg.General.Tun.Stack != C.TunGvisor {
		t.Fatalf("TUN stack = %v, want gVisor", cfg.General.Tun.Stack)
	}
	if cfg.General.FindProcessMode != process.FindProcessOff {
		t.Fatalf("find process mode = %v, want off", cfg.General.FindProcessMode)
	}
	if cfg.General.IPv6 || cfg.DNS.IPv6 {
		t.Fatal("IPv6 remained enabled")
	}
	if len(cfg.General.Tun.Inet6Address) != 0 || len(cfg.General.Tun.Inet6RouteAddress) != 0 {
		t.Fatal("IPv6 TUN fields were not cleared")
	}
	if len(cfg.General.Tun.RouteAddress) != 1 || !cfg.General.Tun.RouteAddress[0].Addr().Is4() {
		t.Fatalf("generic routes were not reduced to IPv4: %v", cfg.General.Tun.RouteAddress)
	}
	if len(cfg.General.Tun.LoopbackAddress) != 1 || !cfg.General.Tun.LoopbackAddress[0].Is4() {
		t.Fatalf("loopback addresses were not reduced to IPv4: %v", cfg.General.Tun.LoopbackAddress)
	}
}

func TestApplyRuntimeOverridesEnablesStrictProcessMatching(t *testing.T) {
	cfg := &config.Config{
		General: &config.General{
			FindProcessMode: process.FindProcessOff,
			Inbound: config.Inbound{Tun: LC.Tun{
				Enable:       true,
				Inet4Address: []netip.Prefix{netip.MustParsePrefix("198.18.0.1/30")},
			}},
		},
	}
	if err := applyRuntimeOverridesForOptions(cfg, RuntimeOverrides{
		ProcessMatching: true,
		IPv6Enabled:     true,
	}); err != nil {
		t.Fatalf("applyRuntimeOverridesForOptions: %v", err)
	}
	if cfg.General.FindProcessMode != process.FindProcessStrict {
		t.Fatalf("find process mode = %v, want strict", cfg.General.FindProcessMode)
	}
	if cfg.General.Tun.Stack != C.TunGvisor {
		t.Fatalf("TUN stack = %v, want gVisor", cfg.General.Tun.Stack)
	}
}
