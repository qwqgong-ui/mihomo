//go:build android

package androidplatform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/metacubex/mihomo/component/process"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
)

const (
	embeddedIPv4Prefix = "172.19.0.1/30"
	embeddedIPv6Prefix = "fdfe:dcba:9876::1/126"
	embeddedMTU        = 9000
)

// EmbeddedOptions describes the Android VpnService contract used by the JNI
// runtime. The Java service owns routes and package policy; mihomo owns packet
// processing on the supplied descriptor.
type EmbeddedOptions struct {
	FileDescriptor  int
	Stack           string
	IPv6Enabled     bool
	ProcessMatching bool
}

// PrepareEmbeddedConfig applies the Android JNI runtime contract to a parsed
// configuration and returns the exact VpnService.Builder options as JSON.
// FileDescriptor may be negative during the prepare-only pass.
func PrepareEmbeddedConfig(cfg *config.Config, options EmbeddedOptions) ([]byte, error) {
	if cfg == nil || cfg.General == nil {
		return nil, errors.New("android embedded runtime received an incomplete configuration")
	}

	stackName := strings.ToLower(strings.TrimSpace(options.Stack))
	if stackName == "" {
		stackName = "system"
	}
	stack, found := C.StackTypeMapping[stackName]
	if !found {
		return nil, fmt.Errorf("unsupported Android TUN stack: %s", options.Stack)
	}

	tunConfig := &cfg.General.Tun
	if len(tunConfig.RouteAddressSet) != 0 || len(tunConfig.RouteExcludeAddressSet) != 0 {
		return nil, errors.New("Android VpnService does not support dynamic TUN route-address-set fields")
	}

	originalAutoRoute := tunConfig.AutoRoute
	tunConfig.Enable = true
	tunConfig.Device = "AndroidCyaml"
	tunConfig.Stack = stack
	tunConfig.MTU = embeddedMTU
	tunConfig.GSO = false
	tunConfig.GSOMaxSize = 0
	tunConfig.Inet4Address = []netip.Prefix{netip.MustParsePrefix(embeddedIPv4Prefix)}
	if options.IPv6Enabled {
		tunConfig.Inet6Address = []netip.Prefix{netip.MustParsePrefix(embeddedIPv6Prefix)}
		cfg.General.IPv6 = true
	} else {
		cfg.General.IPv6 = false
		tunConfig.Inet6Address = nil
		tunConfig.Inet6RouteAddress = nil
		tunConfig.Inet6RouteExcludeAddress = nil
		tunConfig.RouteAddress = ipv4Prefixes(tunConfig.RouteAddress)
		tunConfig.RouteExcludeAddress = ipv4Prefixes(tunConfig.RouteExcludeAddress)
		tunConfig.LoopbackAddress = ipv4Addresses(tunConfig.LoopbackAddress)
	}
	if cfg.DNS != nil {
		cfg.DNS.IPv6 = cfg.DNS.IPv6 && options.IPv6Enabled
	}

	if options.ProcessMatching {
		cfg.General.FindProcessMode = process.FindProcessAlways
	} else {
		cfg.General.FindProcessMode = process.FindProcessOff
	}

	// The prepare pass must preserve the user's route intent so Android can
	// build the matching VPN profile before the Go listener starts.
	tunConfig.AutoRoute = originalAutoRoute
	dnsEnabled := cfg.DNS != nil && cfg.DNS.Enable
	spec := makeTunSpec(*tunConfig, dnsEnabled)
	payload, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("encode Android TUN options: %w", err)
	}

	if options.FileDescriptor >= 0 {
		tunConfig.FileDescriptor = options.FileDescriptor
		tunConfig.AutoRoute = false
		tunConfig.AutoRedirect = false
		tunConfig.AutoDetectInterface = false
		tunConfig.IncludePackage = nil
		tunConfig.ExcludePackage = nil
		tunConfig.IncludeAndroidUser = nil
		tunConfig.IncludeUID = nil
		tunConfig.IncludeUIDRange = nil
		tunConfig.ExcludeUID = nil
		tunConfig.ExcludeUIDRange = nil
	}
	return payload, nil
}
