package androidcyaml

import (
	"github.com/metacubex/http"
	"github.com/metacubex/mihomo/transport/xhttp"
)

// BrowserTransportOptions carries the node's configured server address and Host
// down to the platform transport, so a browser-owned request can still reach the
// exact endpoint the configuration names.
type BrowserTransportOptions = xhttp.ExternalTransportOptions

// BrowserTransportFactory builds one RoundTripper per XHTTP node.
type BrowserTransportFactory func(BrowserTransportOptions) (http.RoundTripper, error)

// SetBrowserTransport routes XHTTP's HTTP layer through a platform-supplied
// transport. AndroidCyaml points it at System WebView so that TLS, the HTTP/2
// header set and connection reuse are produced by the installed Chromium rather
// than by Go, while mihomo keeps XHTTP framing, session identifiers and upload
// mode.
//
// supportsStreamUp reports whether the platform transport can send a streaming
// request body. It is detected on the device rather than assumed: when it is
// false, mihomo downgrades stream-up and stream-one to packet-up instead of
// buffering a whole tunnel body in memory.
func SetBrowserTransport(factory BrowserTransportFactory, supportsStreamUp bool) {
	if factory == nil {
		ResetBrowserTransport()
		return
	}
	xhttp.SetExternalTransportFactory(
		xhttp.ExternalTransportFactory(factory),
		xhttp.ExternalTransportCapabilities{StreamUp: supportsStreamUp},
	)
}

// ResetBrowserTransport restores mihomo's native XHTTP transport.
func ResetBrowserTransport() {
	xhttp.SetExternalTransportFactory(nil, xhttp.ExternalTransportCapabilities{})
}
