package outbound

import (
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// hybridGoldenCases are the canonical HQV3 control messages. The same bytes are
// checked into the server repository, where the server's own parsers have to
// decode them: a drift on either side breaks the other side's test rather than
// showing up as a silent interop failure at run time.
var hybridGoldenCases = []struct {
	name    string
	relay   bool
	target  hybridTarget
	payload string
}{
	{name: "domain", target: hybridTarget{name: "example.com", port: 443}, payload: "c000000001"},
	{name: "ipv4", target: hybridTarget{addr: netip.MustParseAddr("1.1.1.1"), port: 443}, payload: "c000000001"},
	{name: "ipv6", target: hybridTarget{addr: netip.MustParseAddr("2606:4700:4700::1111"), port: 443}, payload: "c000000001"},
	// A relayed packet names the flow and nothing else. The payload is a 1-RTT
	// packet, which is what the tunnel carries during the handover.
	{name: "relay", relay: true, target: hybridTarget{addr: netip.MustParseAddr("1.1.1.1"), port: 443}, payload: "42aabbccdd00"},
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
			message := flow.initialMessage(payload)
			if test.relay {
				message = flow.relayMessage(payload)
			}
			got := hex.EncodeToString(message)

			path := filepath.Join("testdata", "hqv3_"+test.name+".hex")
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
				t.Fatalf("HQV3 %s message changed\n got: %s\nwant: %s", test.name, got, trimmed)
			}
		})
	}
}
