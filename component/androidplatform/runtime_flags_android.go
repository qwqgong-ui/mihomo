//go:build android

package androidplatform

import (
	"flag"

	"github.com/metacubex/mihomo/config"
)

var activeRuntimeOverrides = RuntimeOverrides{
	ProcessMatching: true,
	IPv6Enabled:     true,
}

func init() {
	flag.BoolVar(
		&activeRuntimeOverrides.ProcessMatching,
		"android-process-matching",
		true,
		"enable Android endpoint-aware process matching",
	)
	flag.BoolVar(
		&activeRuntimeOverrides.IPv6Enabled,
		"android-ipv6",
		true,
		"enable IPv6 in the Android runtime configuration",
	)
}

func applyRuntimeOverrides(cfg *config.Config) error {
	return applyRuntimeOverridesForOptions(cfg, activeRuntimeOverrides)
}
