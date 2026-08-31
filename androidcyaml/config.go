package androidcyaml

import (
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub/executor"
	LC "github.com/metacubex/mihomo/listener/config"
	"github.com/metacubex/mihomo/log"
)

// The config types AndroidCyaml is allowed to touch, aliased so that a rename
// upstream lands here rather than across the wrapper. AndroidCyaml shapes the
// parsed config in memory to apply its fixed Android TUN contract -- that
// transformation is platform policy and deliberately stays in AndroidCyaml, so
// these types have to remain reachable. Aliasing keeps the blast radius at one
// file without moving policy into mihomo.
type (
	Config    = config.Config
	RawConfig = config.RawConfig
	Tun       = LC.Tun
	LogLevel  = log.LogLevel
)

// TunStackSystem is the only stack AndroidCyaml runs. gVisor and mixed are not
// compiled into the Android core.
const TunStackSystem = C.TunSystem

// SetPaths points the core at AndroidCyaml's private working directory before
// anything reads a relative path out of it.
func SetPaths(homeDir, configPath string) error {
	C.SetHomeDir(homeDir)
	C.SetConfig(configPath)
	return config.Init(homeDir)
}

// ParseConfigBytes validates and parses a candidate configuration without
// touching the running core. AndroidCyaml uses it to reject a bad import before
// it can replace the file on disk.
func ParseConfigBytes(configuration []byte) (*Config, error) {
	return executor.ParseWithBytes(configuration)
}

// ParseConfigPath parses the configuration the core is about to run.
func ParseConfigPath(path string) (*Config, error) {
	return executor.ParseWithPath(path)
}

// UnmarshalRawConfig exposes the pre-resolution document. AndroidCyaml reads it
// to find the first configured Selector, which is the only group its
// per-network memory ever touches.
func UnmarshalRawConfig(configuration []byte) (*RawConfig, error) {
	return config.UnmarshalRawConfig(configuration)
}

// DefaultRawConfig is the baseline AndroidCyaml compares an imported document
// against.
func DefaultRawConfig() *RawConfig {
	return config.DefaultRawConfig()
}

// ParseLogLevel maps a wire value from the AndroidCyaml override panel onto a
// mihomo log level. The second result is false for an unknown name.
func ParseLogLevel(name string) (LogLevel, bool) {
	level, found := log.LogLevelMapping[name]
	return level, found
}
