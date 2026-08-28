// Package androidcyaml is the private integration facade consumed by
// qwqgong-ui/AndroidCyaml. Platform implementations remain in that repository;
// this package only wires them into mihomo's neutral extension points.
package androidcyaml

import (
	"net/netip"

	"github.com/metacubex/http"
	"github.com/metacubex/mihomo/component/process"
	"github.com/metacubex/mihomo/transport/xhttp"
)

type ProcessResolver func(network string, src, dst netip.AddrPort) (uint32, string, error)

type BrowserTransportOptions = xhttp.ExternalTransportOptions

type BrowserTransportFactory func(BrowserTransportOptions) (http.RoundTripper, error)

func SetProcessResolver(resolver ProcessResolver) {
	process.SetEndpointResolver(process.EndpointResolver(resolver))
}

func SetBrowserTransport(factory BrowserTransportFactory, supportsStreamUp bool) {
	xhttp.SetExternalTransportFactory(
		xhttp.ExternalTransportFactory(factory),
		xhttp.ExternalTransportCapabilities{StreamUp: supportsStreamUp},
	)
}

func ResetProcessResolver() {
	process.SetEndpointResolver(nil)
}

func ResetBrowserTransport() {
	xhttp.SetExternalTransportFactory(nil, xhttp.ExternalTransportCapabilities{})
}
