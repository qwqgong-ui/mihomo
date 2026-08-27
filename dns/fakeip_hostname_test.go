package dns

import (
	"strings"
	"testing"

	D "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func TestIsFakeIPHostName(t *testing.T) {
	for _, name := range []string{
		"www.baidu.com",
		"router",
		"_dmarc.example.com",
		"_http._tcp.example.com",
		"xn--fiqs8s",
		"a-b.example-1.com",
		"EXAMPLE.COM",
	} {
		require.True(t, isFakeIPHostName(name), name)
	}
	for _, name := range []string{
		"",
		// What ping makes of a URL: the wire name carries bytes that cannot
		// appear in a host name, and miekg/dns escapes them on the way out.
		`https\058\047\047www.baidu.com`,
		"https://www.baidu.com",
		"www.baidu.com/generate_204",
		"example .com",
		"example..com",
		"example.com:443",
		strings.Repeat("a", 64) + ".com",
		strings.Repeat("a.", 127) + "com",
	} {
		require.False(t, isFakeIPHostName(name), name)
	}
}

func TestHandleMsgWithNameError(t *testing.T) {
	request := &D.Msg{}
	request.SetQuestion(D.Fqdn("example.com"), D.TypeA)
	response := handleMsgWithNameError(request)
	require.Equal(t, D.RcodeNameError, response.Rcode)
	require.Empty(t, response.Answer)
	require.True(t, response.Authoritative)
}
