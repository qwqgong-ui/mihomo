package androidplatform

import (
	"errors"
	"net/netip"

	"github.com/metacubex/mihomo/component/process"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
)

type RuntimeOverrides struct {
	ProcessMatching bool
	IPv6Enabled     bool
}

func applyRuntimeOverridesForOptions(cfg *config.Config, options RuntimeOverrides) error {
	if cfg == nil || cfg.General == nil {
		return errors.New("android runtime override received an incomplete configuration")
	}

	// AndroidCyaml only packages the gVisor-capable core. Do not expose system or
	// mixed as runtime choices because they cannot be supported consistently by
	// the Android VpnService process model.
	cfg.General.Tun.Stack = C.TunGvisor
	if options.ProcessMatching {
		cfg.General.FindProcessMode = process.FindProcessStrict
	} else {
		cfg.General.FindProcessMode = process.FindProcessOff
	}

	if options.IPv6Enabled {
		return nil
	}

	cfg.General.IPv6 = false
	if cfg.DNS != nil {
		cfg.DNS.IPv6 = false
	}

	tunConfig := &cfg.General.Tun
	tunConfig.Inet6Address = nil
	tunConfig.Inet6RouteAddress = nil
	tunConfig.Inet6RouteExcludeAddress = nil
	tunConfig.RouteAddress = ipv4Prefixes(tunConfig.RouteAddress)
	tunConfig.RouteExcludeAddress = ipv4Prefixes(tunConfig.RouteExcludeAddress)
	tunConfig.LoopbackAddress = ipv4Addresses(tunConfig.LoopbackAddress)
	if len(tunConfig.Inet4Address) == 0 {
		return errors.New("IPv6 is disabled but the TUN configuration has no IPv4 address")
	}
	return nil
}

func ipv4Prefixes(values []netip.Prefix) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		if value.IsValid() && value.Addr().Is4() {
			result = append(result, value)
		}
	}
	return result
}

func ipv4Addresses(values []netip.Addr) []netip.Addr {
	result := make([]netip.Addr, 0, len(values))
	for _, value := range values {
		if value.IsValid() && value.Is4() {
			result = append(result, value)
		}
	}
	return result
}
