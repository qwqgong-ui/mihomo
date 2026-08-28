package xhttp

import (
	"errors"
	"testing"

	"github.com/metacubex/http"
)

type externalRoundTripFunc func(*http.Request) (*http.Response, error)

func (f externalRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestExternalTransportFactory(t *testing.T) {
	SetExternalTransportFactory(nil, ExternalTransportCapabilities{StreamUp: true})
	t.Cleanup(func() { SetExternalTransportFactory(nil, ExternalTransportCapabilities{}) })
	if ExternalTransportEnabled() || ExternalTransportSupportsStreamUp() {
		t.Fatal("external transport unexpectedly enabled")
	}
	wantErr := errors.New("test transport reached")
	want := ExternalTransportOptions{ServerAddress: "203.0.113.10:443", Host: "example.com"}
	SetExternalTransportFactory(func(options ExternalTransportOptions) (http.RoundTripper, error) {
		if options != want {
			t.Fatalf("options = %#v, want %#v", options, want)
		}
		return externalRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, wantErr }), nil
	}, ExternalTransportCapabilities{StreamUp: true})
	if !ExternalTransportEnabled() || !ExternalTransportSupportsStreamUp() {
		t.Fatal("external transport not enabled")
	}
	request, err := http.NewRequest(http.MethodGet, "https://example.com/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ExternalTransportMaker(want)().RoundTrip(request); !errors.Is(err, wantErr) {
		t.Fatalf("RoundTrip error = %v, want %v", err, wantErr)
	}
}

func TestPrepareExternalTransportConfig(t *testing.T) {
	SetExternalTransportFactory(func(ExternalTransportOptions) (http.RoundTripper, error) { return nil, nil }, ExternalTransportCapabilities{StreamUp: true})
	t.Cleanup(func() { SetExternalTransportFactory(nil, ExternalTransportCapabilities{}) })
	config := &Config{Mode: "stream-one", SessionPlacement: PlacementQuery, SeqPlacement: PlacementQuery, XPaddingPlacement: PlacementQueryInHeader, ReuseConfig: &ReuseConfig{}}
	if err := PrepareExternalTransportConfig(config); err != nil {
		t.Fatal(err)
	}
	if config.Mode != "stream-up" || config.ReuseConfig == nil {
		t.Fatalf("unexpected prepared config: %#v", config)
	}
	config.Headers = map[string]string{"Cookie": "x=y"}
	if err := PrepareExternalTransportConfig(config); err == nil {
		t.Fatal("cookie config unexpectedly accepted")
	}
}
