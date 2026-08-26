//go:build no_wireguard

package outbound

import "fmt"

type WireGuard struct {
	*Base
}

type WireGuardOption struct {
	BasicOption
	WireGuardPeerOption
	Name                string `proxy:"name"`
	Ip                  string `proxy:"ip,omitempty"`
	Ipv6                string `proxy:"ipv6,omitempty"`
	PrivateKey          string `proxy:"private-key"`
	Workers             int    `proxy:"workers,omitempty"`
	MTU                 int    `proxy:"mtu,omitempty"`
	UDP                 bool   `proxy:"udp,omitempty"`
	PersistentKeepalive int    `proxy:"persistent-keepalive,omitempty"`

	IPStack IPStackOption `proxy:"ip-stack,omitempty"`

	AmneziaWGOption *AmneziaWGOption `proxy:"amnezia-wg-option,omitempty"`

	Peers []WireGuardPeerOption `proxy:"peers,omitempty"`

	RemoteDnsResolve bool     `proxy:"remote-dns-resolve,omitempty"`
	Dns              []string `proxy:"dns,omitempty"`

	RefreshServerIPInterval int `proxy:"refresh-server-ip-interval,omitempty"`
}

type WireGuardPeerOption struct {
	Server       string   `proxy:"server,omitempty"`
	Port         int      `proxy:"port,omitempty"`
	PublicKey    string   `proxy:"public-key,omitempty"`
	PreSharedKey string   `proxy:"pre-shared-key,omitempty"`
	Reserved     []uint8  `proxy:"reserved,omitempty"`
	AllowedIPs   []string `proxy:"allowed-ips,omitempty"`
}

type AmneziaWGOption struct {
	Version int `proxy:"version,omitempty"`

	JC   int `proxy:"jc,omitempty"`
	JMin int `proxy:"jmin,omitempty"`
	JMax int `proxy:"jmax,omitempty"`
	S1   int `proxy:"s1,omitempty"`
	S2   int `proxy:"s2,omitempty"`
	S3   int `proxy:"s3,omitempty"`
	S4   int `proxy:"s4,omitempty"`

	H1 string `proxy:"h1,omitempty"`
	H2 string `proxy:"h2,omitempty"`
	H3 string `proxy:"h3,omitempty"`
	H4 string `proxy:"h4,omitempty"`

	I1 string `proxy:"i1,omitempty"`
	I2 string `proxy:"i2,omitempty"`
	I3 string `proxy:"i3,omitempty"`
	I4 string `proxy:"i4,omitempty"`
	I5 string `proxy:"i5,omitempty"`

	J1    string `proxy:"j1,omitempty"`
	J2    string `proxy:"j2,omitempty"`
	J3    string `proxy:"j3,omitempty"`
	Itime int64  `proxy:"itime,omitempty"`

	HeaderProtectionKey    string `proxy:"header-protection-key,omitempty"`
	ContentPaddingAddition string `proxy:"content-padding-addition,omitempty"`
	RekeyAfterTime         string `proxy:"rekey-after-time,omitempty"`
	RekeyTimeout           string `proxy:"rekey-timeout,omitempty"`
	RejectAfterTime        string `proxy:"reject-after-time,omitempty"`
	KeepaliveTimeout       string `proxy:"keepalive-timeout,omitempty"`
	MaxHandshakeAttempts   string `proxy:"max-handshake-attempts,omitempty"`
	RandomTrailers         bool   `proxy:"random-trailers,omitempty"`
	DisableCookies         bool   `proxy:"disable-cookies,omitempty"`
}

func NewWireGuard(WireGuardOption) (*WireGuard, error) {
	return nil, fmt.Errorf("wireguard support is disabled by \"no_wireguard\" build tag")
}
