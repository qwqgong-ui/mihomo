package dns

import (
	"strings"

	D "github.com/miekg/dns"
)

const maxHostNameLength = 253

// isFakeIPHostName reports whether a queried name can be a host name at all.
//
// Fake-IP answers A/AAAA out of the pool without ever asking an upstream, so
// whatever the client asked for gets an address - including strings no name
// server would ever resolve, such as a URL handed straight to ping or curl.
// The address that comes back is then dead for every protocol: DIRECT fails to
// resolve the name at dial time, and the user is left looking at a routing
// problem that is really a typo. Answering NXDOMAIN puts the failure where it
// happened, and matches what the client would see with no tunnel at all.
//
// The check is deliberately about characters rather than about registered
// names: underscores (_dmarc, _http._tcp), single labels (a LAN host name) and
// punycode are all legitimate queries, while an escape sequence in the
// presentation form means the wire name carried a byte that cannot appear in a
// host name.
func isFakeIPHostName(name string) bool {
	if name == "" || len(name) > maxHostNameLength {
		return false
	}
	for label := range strings.SplitSeq(name, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for index := 0; index < len(label); index++ {
			switch character := label[index]; {
			case character >= 'a' && character <= 'z':
			case character >= 'A' && character <= 'Z':
			case character >= '0' && character <= '9':
			case character == '-' || character == '_':
			default:
				return false
			}
		}
	}
	return true
}

func handleMsgWithNameError(r *D.Msg) *D.Msg {
	msg := &D.Msg{}
	msg.SetRcode(r, D.RcodeNameError)
	msg.Authoritative = true
	msg.RecursionAvailable = true
	return msg
}
