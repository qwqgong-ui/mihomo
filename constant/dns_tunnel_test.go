package constant

import "testing"

// The reserved name is answered inside the proxy on both ends. It must never
// be resolved locally, and it must never swallow a real host.
func TestIsTunnelDNSHost(t *testing.T) {
	if !IsTunnelDNSHost(TunnelDNSHost) {
		t.Fatal("the reserved host must be recognised")
	}
	for _, host := range []string{
		"", "example.com", "server-dns.invalid.example.com",
		"evil-server-dns.invalid", "x.server-dns.invalid", TunnelDNSAddress,
	} {
		if IsTunnelDNSHost(host) {
			t.Fatalf("%q must not be treated as the reserved host", host)
		}
	}
	if TunnelDNSAddress != TunnelDNSHost+":"+TunnelDNSPort {
		t.Fatal("address must be host and port joined")
	}
}

// Asking a local outbound for the server's view of a record is meaningless:
// there is no server behind it.
func TestCanServeTunnelDNS(t *testing.T) {
	for _, at := range []AdapterType{Direct, Reject, RejectDrop, Compatible, Pass, PassRule, Rematch, Dns} {
		if at.CanServeTunnelDNS() {
			t.Fatalf("%s has no proxy server to ask", at)
		}
	}
	for _, at := range []AdapterType{Shadowsocks, Vmess, Vless, Trojan, Hysteria2, Tuic, WireGuard, AnyTLS} {
		if !at.CanServeTunnelDNS() {
			t.Fatalf("%s should be able to serve tunnel DNS", at)
		}
	}
}
