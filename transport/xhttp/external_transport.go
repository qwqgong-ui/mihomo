package xhttp

import (
	"errors"
	"strings"
	"sync"

	"github.com/metacubex/http"
)

type ExternalTransportOptions struct {
	ServerAddress string
	Host          string
}

type ExternalTransportCapabilities struct {
	StreamUp bool
}

type ExternalTransportFactory func(ExternalTransportOptions) (http.RoundTripper, error)

var externalTransportState struct {
	sync.RWMutex
	factory      ExternalTransportFactory
	capabilities ExternalTransportCapabilities
}

func SetExternalTransportFactory(factory ExternalTransportFactory, capabilities ExternalTransportCapabilities) {
	externalTransportState.Lock()
	externalTransportState.factory = factory
	if factory == nil {
		capabilities = ExternalTransportCapabilities{}
	}
	externalTransportState.capabilities = capabilities
	externalTransportState.Unlock()
}

func ExternalTransportEnabled() bool {
	externalTransportState.RLock()
	enabled := externalTransportState.factory != nil
	externalTransportState.RUnlock()
	return enabled
}

func ExternalTransportSupportsStreamUp() bool {
	externalTransportState.RLock()
	supported := externalTransportState.factory != nil && externalTransportState.capabilities.StreamUp
	externalTransportState.RUnlock()
	return supported
}

func NewExternalTransport(options ExternalTransportOptions) (http.RoundTripper, error) {
	externalTransportState.RLock()
	factory := externalTransportState.factory
	externalTransportState.RUnlock()
	if factory == nil {
		return nil, errors.New("xhttp external transport is not installed")
	}
	return factory(options)
}

type externalErrorTransport struct{ err error }

func (t externalErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

func ExternalTransportMaker(options ExternalTransportOptions) TransportMaker {
	return func() http.RoundTripper {
		transport, err := NewExternalTransport(options)
		if err != nil {
			return externalErrorTransport{err: err}
		}
		return transport
	}
}

func PrepareExternalTransportConfig(config *Config) error {
	if config == nil {
		return errors.New("xhttp external transport received nil config")
	}
	if config.GetNormalizedSessionPlacement() == PlacementCookie ||
		config.GetNormalizedSeqPlacement() == PlacementCookie ||
		config.XPaddingPlacement == PlacementCookie ||
		config.GetNormalizedUplinkDataPlacement() == PlacementCookie {
		return errors.New("xhttp external transport does not support cookie placement")
	}
	for name := range config.Headers {
		if strings.EqualFold(name, "Cookie") {
			return errors.New("xhttp external transport does not support custom Cookie headers")
		}
	}
	mode := config.NormalizedMode()
	if mode == "stream-one" && ExternalTransportSupportsStreamUp() {
		config.Mode = "stream-up"
	} else if mode != "stream-up" || !ExternalTransportSupportsStreamUp() {
		config.Mode = "packet-up"
		config.ReuseConfig = nil
	}
	return nil
}
