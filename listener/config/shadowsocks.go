package config

import (
	"github.com/metacubex/mihomo/listener/sing"

	"encoding/json"
)

type ShadowsocksServer struct {
	Enable     bool
	Listen     string
	Password   string
	Cipher     string
	Udp        bool
	MuxOption  sing.MuxOption `yaml:"mux-option" json:"mux-option"`
	ShadowTLS  ShadowTLS      `yaml:"shadow-tls" json:"shadow-tls"`
	ResTLS     ResTLS         `yaml:"res-tls" json:"res-tls"`
	JLSConfig  JLSConfig      `yaml:"jls-config" json:"jls-config"`
	KcpTun     KcpTun         `yaml:"kcp-tun" json:"kcp-tun"`
	SimpleObfs SimpleObfs     `yaml:"simple-obfs" json:"simple-obfs"`
}

type SimpleObfs struct {
	Enable bool
	Mode   string
}

func (t ShadowsocksServer) String() string {
	b, _ := json.Marshal(t)
	return string(b)
}
