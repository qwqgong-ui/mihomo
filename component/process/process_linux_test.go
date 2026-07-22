package process

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func socketInode(t testing.TB, conn syscall.Conn) uint32 {
	t.Helper()
	rawConn, err := conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var target string
	var readErr error
	if err := rawConn.Control(func(fd uintptr) {
		target, readErr = os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(fd), 10))
	}); err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	inodeText := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
	inode, err := strconv.ParseUint(inodeText, 10, 32)
	if err != nil {
		t.Fatalf("parse socket inode from %q: %v", target, err)
	}
	return uint32(inode)
}

func testExactTCP(t *testing.T, network, address string) {
	t.Helper()
	listener, err := net.ListenTCP(network, mustResolveTCPAddr(t, network, address))
	if err != nil {
		t.Skipf("listen %s: %v", network, err)
	}
	defer listener.Close()

	accepted := make(chan *net.TCPConn, 1)
	go func() {
		conn, acceptErr := listener.AcceptTCP()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	client, err := net.DialTCP(network, nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()

	src := client.LocalAddr().(*net.TCPAddr).AddrPort()
	dst := client.RemoteAddr().(*net.TCPAddr).AddrPort()
	uid, inode, err := resolveSocketByNetlinkExact("tcp", src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint32(os.Getuid()); uid != want {
		t.Fatalf("uid = %d, want %d", uid, want)
	}
	if want := socketInode(t, client); inode != want {
		t.Fatalf("inode = %d, want client inode %d", inode, want)
	}
}

func mustResolveTCPAddr(t testing.TB, network, address string) *net.TCPAddr {
	t.Helper()
	addr, err := net.ResolveTCPAddr(network, address)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestResolveSocketByNetlinkExactTCP(t *testing.T) {
	t.Run("ipv4", func(t *testing.T) { testExactTCP(t, "tcp4", "127.0.0.1:0") })
	t.Run("ipv6", func(t *testing.T) { testExactTCP(t, "tcp6", "[::1]:0") })
}

func TestResolveSocketByNetlinkExactUDP(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	dst := server.LocalAddr().(*net.UDPAddr).AddrPort()

	t.Run("connected", func(t *testing.T) {
		client, err := net.DialUDP("udp4", nil, server.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		if _, err := client.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		src := client.LocalAddr().(*net.UDPAddr).AddrPort()
		uid, inode, err := resolveSocketByNetlinkExact("udp", src, dst)
		if err != nil {
			t.Fatal(err)
		}
		if want := uint32(os.Getuid()); uid != want {
			t.Fatalf("uid = %d, want %d", uid, want)
		}
		if want := socketInode(t, client); inode != want {
			t.Fatalf("inode = %d, want client inode %d", inode, want)
		}
	})

	t.Run("unconnected-wildcard", func(t *testing.T) {
		client, err := net.ListenUDP("udp4", &net.UDPAddr{})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		if _, err := client.WriteToUDP([]byte{1}, server.LocalAddr().(*net.UDPAddr)); err != nil {
			t.Fatal(err)
		}
		local := client.LocalAddr().(*net.UDPAddr).AddrPort()
		src := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), local.Port())
		uid, inode, err := resolveSocketByNetlinkExact("udp", src, dst)
		if err != nil {
			t.Fatal(err)
		}
		if want := uint32(os.Getuid()); uid != want {
			t.Fatalf("uid = %d, want %d", uid, want)
		}
		if want := socketInode(t, client); inode != want {
			t.Fatalf("inode = %d, want client inode %d", inode, want)
		}
	})
}

func TestFindProcessNameByAddrFallsBack(t *testing.T) {
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
	wrongPort := dst.Port() + 1
	if wrongPort == 0 {
		wrongPort = dst.Port() - 1
	}
	wrongDst := netip.AddrPortFrom(dst.Addr(), wrongPort)
	if _, _, err := resolveSocketByNetlinkExact("tcp", src, wrongDst); err == nil {
		t.Fatal("exact lookup unexpectedly found a socket with the wrong destination")
	}
	uid, path, err := FindProcessNameByAddr("tcp", src, wrongDst)
	if err != nil {
		t.Fatal(err)
	}
	if uid != uint32(os.Getuid()) || path == "" {
		t.Fatalf("fallback result uid=%d path=%q", uid, path)
	}
	if _, _, err := resolveSocketByNetlink("tcp", netip.MustParseAddr("192.0.2.1"), int(src.Port())); err == nil {
		t.Fatal("dump lookup unexpectedly returned an unrelated socket")
	}
}

func TestRecentProcessPIDs(t *testing.T) {
	uid := uint32(4_000_000_000)
	recentProcessPIDs.Delete(uid)
	t.Cleanup(func() { recentProcessPIDs.Delete(uid) })

	for i := 0; i < recentPIDLimit+2; i++ {
		rememberProcessPID(uid, fmt.Sprintf("%d", i))
	}

	got := loadRecentProcessPIDs(uid)
	if len(got) != recentPIDLimit {
		t.Fatalf("cache length = %d, want %d", len(got), recentPIDLimit)
	}
	if got[0] != "9" || got[len(got)-1] != "2" {
		t.Fatalf("unexpected MRU order: %v", got)
	}

	rememberProcessPID(uid, "5")
	got = loadRecentProcessPIDs(uid)
	if got[0] != "5" || got[1] != "9" {
		t.Fatalf("existing PID was not promoted: %v", got)
	}
}

func BenchmarkFindProcessNameCachedPID(b *testing.B) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
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
		b.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()

	local := client.LocalAddr().(*net.TCPAddr)
	ip, ok := netip.AddrFromSlice(local.IP)
	if !ok {
		b.Fatal("invalid local IP")
	}

	src := local.AddrPort()
	dst := client.RemoteAddr().(*net.TCPAddr).AddrPort()
	b.Run("dump", func(b *testing.B) {
		for b.Loop() {
			if _, _, err := FindProcessName("tcp", ip.Unmap(), local.Port); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("exact", func(b *testing.B) {
		for b.Loop() {
			if _, _, err := FindProcessNameByAddr("tcp", src, dst); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkFindProcessNameStages(b *testing.B) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
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
		b.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()

	local := client.LocalAddr().(*net.TCPAddr)
	ip, ok := netip.AddrFromSlice(local.IP)
	if !ok {
		b.Fatal("invalid local IP")
	}
	ip = ip.Unmap()
	src := local.AddrPort()
	dst := client.RemoteAddr().(*net.TCPAddr).AddrPort()
	uid, inode, err := resolveSocketByNetlink("tcp", ip, local.Port)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := resolveProcessNameByProcSearch(inode, uid); err != nil {
		b.Fatal(err)
	}

	b.Run("netlink", func(b *testing.B) {
		for b.Loop() {
			if _, _, err := resolveSocketByNetlink("tcp", ip, local.Port); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("netlink-exact", func(b *testing.B) {
		for b.Loop() {
			if _, _, err := resolveSocketByNetlinkExact("tcp", src, dst); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("proc-cached", func(b *testing.B) {
		for b.Loop() {
			if _, err := resolveProcessNameByProcSearch(inode, uid); err != nil {
				b.Fatal(err)
			}
		}
	})
}
