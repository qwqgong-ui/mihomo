package outbound

import (
	"context"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"

	http "github.com/metacubex/http"
	"github.com/metacubex/quic-go"
	"github.com/metacubex/quic-go/http3"
	tls "github.com/metacubex/tls"
)

// TestHybridLive drives a real Hysteria2 tunnel to a real server and runs a real
// QUIC handshake to a real target through it, which is the only way to see the
// parts a virtual fabric cannot reach: quic-go's own connection IDs and their
// lengths, Retry, 0-RTT, and how soon a browser-grade stack rotates a CID.
//
// It is skipped unless HYBRID_LIVE_SERVER is set, so it never runs in CI:
//
//	HYBRID_LIVE_SERVER=host HYBRID_LIVE_PORT=443 HYBRID_LIVE_SNI=name \
//	HYBRID_LIVE_PASSWORD=... HYBRID_LIVE_TARGET=cloudflare-quic.com \
//	go test -run TestHybridLive -v ./adapter/outbound/
func TestHybridLive(t *testing.T) {
	server := os.Getenv("HYBRID_LIVE_SERVER")
	if server == "" {
		t.Skip("set HYBRID_LIVE_SERVER to run the live hybrid QUIC test")
	}
	port, err := strconv.Atoi(envOr("HYBRID_LIVE_PORT", "443"))
	if err != nil {
		t.Fatalf("HYBRID_LIVE_PORT: %v", err)
	}
	target := envOr("HYBRID_LIVE_TARGET", "cloudflare-quic.com")

	option := Hysteria2Option{
		Name:     "hybrid-live",
		Server:   server,
		Port:     port,
		Password: os.Getenv("HYBRID_LIVE_PASSWORD"),
		SNI:      envOr("HYBRID_LIVE_SNI", server),
		ALPN:     []string{"h3"},
	}
	if os.Getenv("HYBRID_LIVE_INSECURE") == "1" {
		option.SkipCertVerify = true
	}

	outbound, err := NewHysteria2(option)
	if err != nil {
		t.Fatalf("build the outbound: %v", err)
	}
	defer outbound.Close()

	if !outbound.hybridQUICEnabled() {
		t.Fatal("hybrid QUIC is gated off for this configuration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	metadata := &C.Metadata{
		NetWork: C.UDP,
		Host:    target,
		DstPort: 443,
	}
	packetConn, err := outbound.ListenPacketContext(ctx, metadata)
	if err != nil {
		t.Fatalf("open the UDP path: %v", err)
	}
	defer packetConn.Close()

	hybrid := isHybridConn(packetConn)
	t.Logf("hybrid relay engaged: %v", hybrid)

	// A real HTTP/3 request over this path exercises the whole handshake: the
	// Initial goes out as a registration, everything after it takes the raw
	// path, and the response has to come back over both.
	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{ServerName: target, NextProtos: []string{"h3"}},
		QUICConfig:      &quic.Config{HandshakeIdleTimeout: 20 * time.Second},
		Dial: func(ctx context.Context, _ string, tlsConfig *tls.Config, quicConfig *quic.Config) (*quic.Conn, error) {
			return quic.DialEarly(ctx, packetConn, &net.UDPAddr{IP: net.IPv4zero, Port: 443}, tlsConfig, quicConfig)
		},
	}
	defer transport.Close()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+target+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("HTTP/3 over the hybrid path failed after %s: %v", time.Since(started), err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		t.Fatalf("read the response body: %v", err)
	}
	t.Logf("HTTP/3 %s in %s, %d bytes, alt-svc=%q",
		response.Status, time.Since(started), len(body), response.Header.Get("alt-svc"))
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func isHybridConn(packetConn C.PacketConn) bool {
	type unwrapper interface{ Unwrap() net.PacketConn }
	var current any = packetConn
	for range 8 {
		if _, ok := current.(*hybridQUICPacketConn); ok {
			return true
		}
		next, ok := current.(unwrapper)
		if !ok {
			return false
		}
		current = next.Unwrap()
	}
	return false
}
