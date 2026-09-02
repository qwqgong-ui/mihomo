package outbound

import (
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// hybridGoldenCases are the canonical HQV2 registrations. The same bytes are
// checked into the server repository, where parseHybridInitial has to decode
// them: a drift on either side breaks the other side's test rather than
// showing up as a silent interop failure at run time.
var hybridGoldenCases = []struct {
	name    string
	target  hybridTarget
	payload string
}{
	{name: "domain", target: hybridTarget{name: "example.com", port: 443}, payload: "c000000001"},
	{name: "ipv4", target: hybridTarget{addr: netip.MustParseAddr("1.1.1.1"), port: 443}, payload: "c000000001"},
	{name: "ipv6", target: hybridTarget{addr: netip.MustParseAddr("2606:4700:4700::1111"), port: 443}, payload: "c000000001"},
}

func TestHybridWireFormatGoldens(t *testing.T) {
	for _, test := range hybridGoldenCases {
		t.Run(test.name, func(t *testing.T) {
			flow := &hybridQUICFlow{target: test.target}
			copy(flow.id[:], "0123456789abcdef")
			payload, err := hex.DecodeString(test.payload)
			if err != nil {
				t.Fatal(err)
			}
			got := hex.EncodeToString(flow.initialMessage(payload))

			path := filepath.Join("testdata", "hqv2_"+test.name+".hex")
			if os.Getenv("HYBRID_GOLDEN_REGEN") == "1" {
				if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if trimmed := string(want[:len(want)-1]); got != trimmed {
				t.Fatalf("HQV2 %s registration changed\n got: %s\nwant: %s", test.name, got, trimmed)
			}
		})
	}
}
