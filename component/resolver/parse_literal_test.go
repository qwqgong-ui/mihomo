package resolver

import (
	"net/netip"
	"testing"
)

// parseAddrLiteral must accept everything netip.ParseAddr accepts; it may only
// short-circuit inputs that would have failed to parse anyway.
func TestParseAddrLiteralMatchesParseAddr(t *testing.T) {
	for _, host := range []string{
		"1.2.3.4",
		"0.0.0.0",
		"255.255.255.255",
		"::",
		"2001:db8::1",
		"::ffff:1.2.3.4",
		"fe80::1%eth0",
		"example.com",
		"host1",
		"1.2.3.4.example.com",
		"xn--fiqs8s",
		"",
		"1.2.3.4.",
		"999.999.999.999",
	} {
		want, wantErr := netip.ParseAddr(host)
		got, gotErr := parseAddrLiteral(host)
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("parseAddrLiteral(%q) error = %v, netip.ParseAddr error = %v", host, gotErr, wantErr)
		}
		if wantErr == nil && got != want {
			t.Fatalf("parseAddrLiteral(%q) = %v, want %v", host, got, want)
		}
	}
}
