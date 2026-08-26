//go:build no_mieru

package outbound

import "fmt"

type Mieru struct {
	*Base
}

type MieruOption struct {
	BasicOption
	Name           string `proxy:"name"`
	Server         string `proxy:"server"`
	Port           int    `proxy:"port,omitempty"`
	PortRange      string `proxy:"port-range,omitempty"`
	Transport      string `proxy:"transport"`
	UDP            bool   `proxy:"udp,omitempty"`
	UserName       string `proxy:"username"`
	Password       string `proxy:"password"`
	Multiplexing   string `proxy:"multiplexing,omitempty"`
	HandshakeMode  string `proxy:"handshake-mode,omitempty"`
	TrafficPattern string `proxy:"traffic-pattern,omitempty"`
}

func NewMieru(MieruOption) (*Mieru, error) {
	return nil, fmt.Errorf("mieru support is disabled by \"no_mieru\" build tag")
}
