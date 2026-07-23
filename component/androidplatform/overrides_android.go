//go:build android

package androidplatform

import (
	C "github.com/metacubex/mihomo/constant"
	LC "github.com/metacubex/mihomo/listener/config"
)

var activeTunStackOverride TunStackOverride

func SetTunStackOverride(value string) error {
	override, err := ParseTunStackOverride(value)
	if err != nil {
		return err
	}
	platformMu.Lock()
	activeTunStackOverride = override
	platformMu.Unlock()
	return nil
}

func applyTunStackOverride(tunConfig *LC.Tun) {
	platformMu.RLock()
	override := activeTunStackOverride
	platformMu.RUnlock()
	if override == TunStackOverrideGVisor {
		tunConfig.Stack = C.TunGvisor
	}
}
