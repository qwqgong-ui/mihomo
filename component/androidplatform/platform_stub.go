//go:build !android

package androidplatform

import (
	"errors"

	"github.com/metacubex/mihomo/config"
	LC "github.com/metacubex/mihomo/listener/config"
)

func Configure(string) error {
	return errors.New("Android platform bridge is only available on Android")
}

func Enabled() bool {
	return false
}

func PrepareConfig(*config.Config) error {
	return nil
}

func PrepareTun(*LC.Tun, bool) error {
	return nil
}
