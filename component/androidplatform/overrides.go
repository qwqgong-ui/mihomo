package androidplatform

import (
	"fmt"
	"strings"
)

type TunStackOverride string

const (
	TunStackOverrideConfig  TunStackOverride = ""
	TunStackOverrideGVisor TunStackOverride = "gvisor"
)

func ParseTunStackOverride(value string) (TunStackOverride, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "config":
		return TunStackOverrideConfig, nil
	case "gvisor":
		return TunStackOverrideGVisor, nil
	case "system":
		return TunStackOverrideConfig, fmt.Errorf("system TUN stack override is unavailable on Android")
	default:
		return TunStackOverrideConfig, fmt.Errorf("unsupported Android TUN stack override: %s", value)
	}
}
