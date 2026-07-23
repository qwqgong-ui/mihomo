//go:build android

package androidplatform

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/metacubex/mihomo/component/process"
	"github.com/metacubex/mihomo/config"
	LC "github.com/metacubex/mihomo/listener/config"
)

var (
	platformMu     sync.RWMutex
	platformSocket string
	openTunMu      sync.Mutex
)

// Configure activates the Android platform bridge. Abstract Unix sockets are
// represented by Go with an '@' prefix.
func Configure(socketName string) error {
	socketName = strings.TrimSpace(socketName)
	if socketName == "" {
		return errors.New("android platform socket is empty")
	}
	if !strings.HasPrefix(socketName, "@") {
		return errors.New("android platform socket must be an abstract Unix socket")
	}
	platformMu.Lock()
	platformSocket = socketName
	platformMu.Unlock()
	process.DefaultProcessNameResolver = resolveProcess
	return nil
}

func Enabled() bool {
	return currentSocketName() != ""
}

// PrepareConfig translates the parsed mihomo TUN configuration into one
// Android VpnService request before any listener is applied.
func PrepareConfig(cfg *config.Config) error {
	if !Enabled() {
		return nil
	}
	if cfg == nil || cfg.General == nil {
		return errors.New("android platform received an incomplete configuration")
	}
	if err := applyRuntimeOverrides(cfg); err != nil {
		return err
	}
	dnsEnabled := cfg.DNS != nil && cfg.DNS.Enable
	return PrepareTun(&cfg.General.Tun, dnsEnabled)
}

func PrepareTun(tunConfig *LC.Tun, dnsEnabled bool) error {
	if !Enabled() {
		return nil
	}
	if tunConfig == nil {
		return errors.New("android platform received a nil TUN configuration")
	}
	if !tunConfig.Enable {
		return errors.New("android VPN mode requires tun.enable: true")
	}
	applyTunStackOverride(tunConfig)
	if len(tunConfig.RouteAddressSet) != 0 || len(tunConfig.RouteExcludeAddressSet) != 0 {
		return errors.New("Android VpnService does not support dynamic TUN route-address-set fields")
	}

	openTunMu.Lock()
	defer openTunMu.Unlock()

	spec := makeTunSpec(*tunConfig, dnsEnabled)
	descriptor, err := openTun(spec)
	if err != nil {
		return fmt.Errorf("android open TUN: %w", err)
	}

	// Android has already applied OS-owned route and package policy. Leaving
	// these switches enabled would ask sing-tun to repeat privileged netlink or
	// package operations from an ordinary application process.
	tunConfig.FileDescriptor = descriptor
	tunConfig.AutoRoute = false
	tunConfig.AutoRedirect = false
	tunConfig.AutoDetectInterface = false
	tunConfig.IncludePackage = nil
	tunConfig.ExcludePackage = nil
	tunConfig.IncludeAndroidUser = nil
	return nil
}

func currentSocketName() string {
	platformMu.RLock()
	defer platformMu.RUnlock()
	return platformSocket
}
