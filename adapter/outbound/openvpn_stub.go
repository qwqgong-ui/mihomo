//go:build no_openvpn

package outbound

import "fmt"

type OpenVPN struct {
	*Base
}

type OpenVPNOption struct {
	BasicOption
	Name               string            `proxy:"name"`
	Server             string            `proxy:"server"`
	Port               int               `proxy:"port"`
	Proto              string            `proxy:"proto,omitempty"`
	Dev                string            `proxy:"dev,omitempty"`
	Cipher             string            `proxy:"cipher,omitempty"`
	DataCiphers        []string          `proxy:"data-ciphers,omitempty"`
	DataCipherFallback string            `proxy:"data-ciphers-fallback,omitempty"`
	Auth               string            `proxy:"auth,omitempty"`
	CompLZO            string            `proxy:"comp-lzo,omitempty"`
	CA                 string            `proxy:"ca"`
	Cert               string            `proxy:"cert,omitempty"`
	Key                string            `proxy:"key,omitempty"`
	TLSAuth            string            `proxy:"tls-auth,omitempty"`
	KeyDirection       string            `proxy:"key-direction,omitempty"`
	TLSCrypt           string            `proxy:"tls-crypt,omitempty"`
	TLSCryptV2         string            `proxy:"tls-crypt-v2,omitempty"`
	Username           string            `proxy:"username,omitempty"`
	Password           string            `proxy:"password,omitempty"`
	PeerInfo           map[string]string `proxy:"peer-info,omitempty"`
	Ping               int               `proxy:"ping,omitempty"`
	PingRestart        int               `proxy:"ping-restart,omitempty"`
	HandshakeTimeout   int               `proxy:"handshake-timeout,omitempty"`
	MTU                int               `proxy:"mtu,omitempty"`
	UDP                bool              `proxy:"udp,omitempty"`

	IPStack IPStackOption `proxy:"ip-stack,omitempty"`

	RemoteDnsResolve bool     `proxy:"remote-dns-resolve,omitempty"`
	Dns              []string `proxy:"dns,omitempty"`
}

func NewOpenVPN(OpenVPNOption) (*OpenVPN, error) {
	return nil, fmt.Errorf("openvpn support is disabled by \"no_openvpn\" build tag")
}
