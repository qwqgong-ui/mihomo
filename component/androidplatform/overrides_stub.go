//go:build !android

package androidplatform

import "errors"

func SetTunStackOverride(value string) error {
	override, err := ParseTunStackOverride(value)
	if err != nil {
		return err
	}
	if override != TunStackOverrideConfig {
		return errors.New("Android TUN stack override is only available on Android")
	}
	return nil
}
