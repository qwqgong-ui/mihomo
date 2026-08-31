package config

import "encoding/json"

type SnellServer struct {
	Listen    string
	Psk       string
	Version   int
	UDP       bool
	ObfsMode  string
	ObfsHost  string
	ShadowTLS ShadowTLS `yaml:"shadow-tls" json:"shadow-tls"`
	ResTLS    ResTLS    `yaml:"res-tls" json:"res-tls"`
	JLSConfig JLSConfig `yaml:"jls-config" json:"jls-config"`
}

func (c SnellServer) String() string {
	b, _ := json.Marshal(c)
	return string(b)
}
