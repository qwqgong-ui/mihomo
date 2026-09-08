package dns

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/component/fakeip"
	icontext "github.com/metacubex/mihomo/context"
	D "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// Set XRAY_BUNDLE_TEST_BINARY to a matching Xray build. All traffic and DNS
// remain on loopback; bundle-test.invalid cannot resolve via the system DNS.
func TestDomainBundleXrayIntegration(t *testing.T) {
	binaryPath := os.Getenv("XRAY_BUNDLE_TEST_BINARY")
	if binaryPath == "" {
		t.Skip("XRAY_BUNDLE_TEST_BINARY is unset")
	}
	const host = "bundle-test.invalid"
	var queries atomic.Int32
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &D.Server{PacketConn: udp, Handler: D.HandlerFunc(func(w D.ResponseWriter, req *D.Msg) {
		queries.Add(1)
		response := new(D.Msg)
		response.SetReply(req)
		var record string
		switch req.Question[0].Qtype {
		case D.TypeA:
			record = host + ". 120 IN A 127.0.0.1"
		case D.TypeHTTPS:
			record = host + `. 60 IN HTTPS 1 . alpn="h3,h2" port=8443 ech=AQID ipv4hint=127.0.0.1`
		}
		if record != "" {
			rr, err := D.NewRR(record)
			if err == nil {
				response.Answer = []D.RR{rr}
			}
		}
		_ = w.WriteMsg(response)
	})}
	tcpDNS, err := net.Listen("tcp", udp.LocalAddr().String())
	require.NoError(t, err)
	tcpServer := &D.Server{Listener: tcpDNS, Handler: server.Handler}
	go func() { _ = tcpServer.ActivateAndServe() }()
	t.Cleanup(func() { _ = tcpServer.Shutdown() })
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	socksPort := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	dnsPort := udp.LocalAddr().(*net.UDPAddr).Port
	config := map[string]any{
		"log":       map[string]any{"loglevel": "debug"},
		"dns":       map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": dnsPort}}, "queryStrategy": "UseIPv4"},
		"inbounds":  []any{map[string]any{"listen": "127.0.0.1", "port": socksPort, "protocol": "socks", "settings": map[string]any{"auth": "noauth"}}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"domainStrategy": "AsIs"}}},
	}
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "xray.json")
	require.NoError(t, os.WriteFile(configPath, raw, 0600))
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	logPath := filepath.Join(t.TempDir(), "xray.log")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	defer logFile.Close()
	t.Cleanup(func() {
		if t.Failed() {
			raw, _ := os.ReadFile(logPath)
			t.Log(string(raw))
		}
	})
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	require.NoError(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	proxyAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort))
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", proxyAddr, 50*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 20*time.Millisecond)
	dial := func(ctx context.Context, target string, port int) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return nil, err
		}
		success := false
		defer func() {
			if !success {
				_ = conn.Close()
			}
		}()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err = conn.Write([]byte{5, 1, 0}); err != nil {
			return nil, err
		}
		var greeting [2]byte
		if _, err = io.ReadFull(conn, greeting[:]); err != nil {
			return nil, err
		}
		if greeting != [2]byte{5, 0} {
			return nil, fmt.Errorf("SOCKS greeting: %v", greeting)
		}
		request := append([]byte{5, 1, 0, 3, byte(len(target))}, []byte(target)...)
		request = binary.BigEndian.AppendUint16(request, uint16(port))
		if _, err = conn.Write(request); err != nil {
			return nil, err
		}
		var header [4]byte
		if _, err = io.ReadFull(conn, header[:]); err != nil {
			return nil, err
		}
		if header[1] != 0 {
			return nil, fmt.Errorf("SOCKS refused: %v", header)
		}
		size := 4
		if header[3] == 4 {
			size = 16
		} else if header[3] == 3 {
			var length [1]byte
			if _, err = io.ReadFull(conn, length[:]); err != nil {
				return nil, err
			}
			size = int(length[0])
		}
		if _, err = io.CopyN(io.Discard, conn, int64(size+2)); err != nil {
			return nil, err
		}
		success = true
		return conn, nil
	}
	client := newDomainClient(&recordingServiceClient{response: &D.Msg{}}, 10)
	client.prepare = func(domain string) (string, func(context.Context) (net.Conn, error), error) {
		if domain != host {
			return "", nil, fmt.Errorf("unexpected host %s", domain)
		}
		return "local-xray", func(ctx context.Context) (net.Conn, error) { return dial(ctx, "server-dns.invalid", 53) }, nil
	}
	pool := newTestFakeIPPool(t, "198.18.0.0/16")
	handler := withFakeIP(&fakeip.Skipper{}, pool, nil, 60, &Resolver{domainClient: client})(func(*icontext.DNSContext, *D.Msg) (*D.Msg, error) { return nil, fmt.Errorf("unexpected fallback") })
	request := new(D.Msg)
	request.SetQuestion(host+".", D.TypeA)
	answer, err := handler(icontext.NewDNSContext(t.Context()), request)
	require.NoError(t, err)
	require.Equal(t, "198.18.0.4", answer.Answer[0].(*D.A).A.String())
	require.EqualValues(t, 2, queries.Load(), "expected one A and one HTTPS at server")
	metadata, err := handler(icontext.NewDNSContext(t.Context()), httpsQuery(host))
	require.NoError(t, err)
	require.Len(t, metadata.Answer, 1)
	values := serviceRecordValues(metadata.Answer[0])
	require.Contains(t, values, D.SVCB_ECHCONFIG)
	require.Equal(t, uint16(8443), values[D.SVCB_PORT].(*D.SVCBPort).Port)
	require.EqualValues(t, 2, queries.Load())
	conn, err := dial(t.Context(), host, echo.Addr().(*net.TCPAddr).Port)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("first connection"))
	require.NoError(t, err)
	payload := make([]byte, len("first connection"))
	_, err = io.ReadFull(conn, payload)
	require.NoError(t, err)
	require.Equal(t, "first connection", string(payload))
	require.EqualValues(t, 2, queries.Load(), "AsIs connection must reuse bundle")
}
