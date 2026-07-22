package process

import (
	"fmt"
	"net"
	"net/netip"
	"testing"
)

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

	b.ResetTimer()
	for b.Loop() {
		if _, _, err := FindProcessName("tcp", ip.Unmap(), local.Port); err != nil {
			b.Fatal(err)
		}
	}
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
	b.Run("proc-cached", func(b *testing.B) {
		for b.Loop() {
			if _, err := resolveProcessNameByProcSearch(inode, uid); err != nil {
				b.Fatal(err)
			}
		}
	})
}
