package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarkAutoEdns0Subnet(t *testing.T) {
	nameservers, err := parseNameServer([]string{"223.5.5.5", "udp://119.29.29.29#ecs=1.2.3.0/24"}, false, false)
	assert.NoError(t, err)

	markAutoEdns0Subnet(nameservers)

	// on by default, without adding ecs-override so a client subnet wins
	assert.Equal(t, "auto", nameservers[0].Params["ecs"])
	assert.NotContains(t, nameservers[0].Params, "ecs-override")
	// an explicit ecs= keeps its own value
	assert.Equal(t, "1.2.3.0/24", nameservers[1].Params["ecs"])
}

func TestDirectNameServerGetsAutoEdns0Subnet(t *testing.T) {
	rawCfg, err := UnmarshalRawConfig([]byte(`
dns:
  enable: true
  nameserver:
    - 114.114.114.114
  direct-nameserver:
    - 223.5.5.5
    - udp://119.29.29.29#ecs=1.2.3.0/24
`))
	assert.NoError(t, err)

	dnsCfg, err := parseDNS(rawCfg, nil)
	assert.NoError(t, err)
	assert.Len(t, dnsCfg.DirectNameServer, 2)
	assert.Equal(t, "auto", dnsCfg.DirectNameServer[0].Params["ecs"])
	assert.Equal(t, "1.2.3.0/24", dnsCfg.DirectNameServer[1].Params["ecs"])
	// other nameserver lists are untouched
	assert.NotContains(t, dnsCfg.NameServer[0].Params, "ecs")
}
