package process

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

type testProcessMatcher func(string) bool

func (m testProcessMatcher) MatchProcess(path string) bool { return m(path) }

func (m testProcessMatcher) MatchProcessName(name string) bool { return m(name) }

func TestFindProcessNameByAddrWithMatcher(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	client, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()

	src := client.LocalAddr().(*net.TCPAddr).AddrPort()
	dst := client.RemoteAddr().(*net.TCPAddr).AddrPort()
	src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
	dst = netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port())

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wantBase := filepath.Base(executable)
	_, path, err := FindProcessNameByAddrWithMatcher(TCP, src, dst, testProcessMatcher(func(path string) bool {
		return filepath.Base(path) == wantBase
	}))
	if err != nil {
		t.Fatalf("matching candidate lookup failed: %v", err)
	}
	if path == "" {
		t.Fatal("matching candidate lookup returned an empty path")
	}

	_, _, err = FindProcessNameByAddrWithMatcher(TCP, src, dst, testProcessMatcher(func(string) bool {
		return false
	}))
	if err == nil {
		t.Fatal("rejecting candidate lookup unexpectedly found a process")
	}
}

func TestEndpointResolverFailureIsAuthoritative(t *testing.T) {
	oldResolver := externalEndpointResolver.Load()
	t.Cleanup(func() { SetEndpointResolver(oldResolver) })
	wantErr := errors.New("platform lookup failed")
	SetEndpointResolver(func(string, netip.AddrPort, netip.AddrPort) (uint32, string, error) {
		return 0, "", wantErr
	})

	_, _, err := FindProcessNameByAddrWithMatcher(
		TCP,
		netip.MustParseAddrPort("127.0.0.1:12345"),
		netip.MustParseAddrPort("127.0.0.1:443"),
		nil,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("endpoint resolver error = %v, want %v", err, wantErr)
	}
}

func TestMatchPackageNameByUID(t *testing.T) {
	oldResolver := DefaultPackageNameResolver
	t.Cleanup(func() { DefaultPackageNameResolver = oldResolver })
	DefaultPackageNameResolver = func(metadata *C.Metadata) (string, error) {
		if metadata.Uid != 10123 {
			return "", fmt.Errorf("unexpected uid %d", metadata.Uid)
		}
		return "com.tencent.mm", nil
	}

	matched, resolved := matchPackageNameByUID(10123, testProcessMatcher(func(name string) bool {
		return name == "com.tencent.mm"
	}))
	if !resolved || !matched {
		t.Fatalf("cached package candidate = (%v, %v), want (true, true)", matched, resolved)
	}

	matched, resolved = matchPackageNameByUID(10123, testProcessMatcher(func(string) bool { return false }))
	if !resolved || matched {
		t.Fatalf("rejected package candidate = (%v, %v), want (false, true)", matched, resolved)
	}
}
