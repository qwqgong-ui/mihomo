package androidcyaml

import (
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/hub/route"
	"github.com/metacubex/mihomo/listener"
)

// SetEmbedMode tells the API server it is hosted inside another process rather
// than owning one.
func SetEmbedMode(embedded bool) {
	route.SetEmbedMode(embedded)
}

// ApplyConfig starts or replaces the running core.
func ApplyConfig(cfg *Config) {
	hub.ApplyConfig(cfg)
}

// Shutdown stops the running core's listeners and resolvers.
func Shutdown() {
	executor.Shutdown()
}

// ResetAPIServer tears down the external controller listener.
func ResetAPIServer() {
	route.ReCreateServer(&route.Config{})
}

// ResetTunListener clears mihomo's cached TUN configuration through its own
// synchronized recreate path.
//
// Shutdown closes the active listener but keeps LastTunConf, because a
// standalone mihomo exits immediately afterwards and the cache never outlives
// it. AndroidCyaml restarts the core in-process, so leaving that cache intact
// lets an identical configuration skip listener creation entirely -- and
// Android is then holding a TUN file descriptor with nothing reading it.
func ResetTunListener() {
	listener.ReCreateTun(Tun{}, nil)
}

// TunConf reports the cached TUN configuration, so a consumer can assert that
// ResetTunListener actually cleared it rather than trusting that it did.
func TunConf() Tun {
	return listener.GetTunConf()
}
