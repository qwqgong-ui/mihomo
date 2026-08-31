package androidcyaml

import (
	"github.com/metacubex/mihomo/component/geodata"
	"github.com/metacubex/mihomo/log"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

// LogEvent is one entry from mihomo's own log stream.
type LogEvent = log.Event

// LogSubscription carries mihomo's own log events.
//
// Every log call publishes here regardless of the configured level, and a slow
// subscriber blocks the call site. A consumer must therefore do nothing but
// classify and count -- never I/O, never a lock another path can hold.
type LogSubscription = <-chan log.Event

// Log severities a subscriber needs to distinguish.
const (
	LogWarning = log.WARNING
	LogError   = log.ERROR
)

// SubscribeLogs starts receiving core log events.
func SubscribeLogs() LogSubscription {
	return log.Subscribe()
}

// UnsubscribeLogs stops receiving core log events.
func UnsubscribeLogs(subscription LogSubscription) {
	log.UnSubscribe(subscription)
}

// Infoln and Warnln let the platform layer report through the same log stream
// the dashboard shows, instead of only to Android's logcat.
func Infoln(format string, values ...any) { log.Infoln(format, values...) }
func Warnln(format string, values ...any) { log.Warnln(format, values...) }

// ConnectionCount reports the number of tracked connections.
func ConnectionCount() uint64 {
	var count uint64
	statistic.DefaultManager.Range(func(statistic.Tracker) bool {
		count++
		return true
	})
	return count
}

// TotalTraffic reports cumulative bytes in each direction.
func TotalTraffic() (uploaded, downloaded int64) {
	return statistic.DefaultManager.Total()
}

// CloseAllConnections closes every tracked connection. AndroidCyaml calls it
// when the physical route changed underneath them, so the existing sockets are
// bound to a path that no longer exists.
func CloseAllConnections() {
	statistic.DefaultManager.Range(func(connection statistic.Tracker) bool {
		_ = connection.Close()
		return true
	})
}

// ClearGeoCaches releases the parsed GeoIP and GeoSite data, which mihomo
// rebuilds on demand. This is the largest reclaimable allocation in the core and
// the first thing to drop when Android asks for memory back.
func ClearGeoCaches() {
	geodata.ClearGeoIPCache()
	geodata.ClearGeoSiteCache()
}
