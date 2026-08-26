//go:build no_mieru

package inbound

import (
	"fmt"

	C "github.com/metacubex/mihomo/constant"
)

type Mieru struct {
	*Base
}

type MieruOption struct {
	BaseOption
	Transport           string            `inbound:"transport"`
	Users               map[string]string `inbound:"users"`
	TrafficPattern      string            `inbound:"traffic-pattern,omitempty"`
	UserHintIsMandatory bool              `inbound:"user-hint-is-mandatory,omitempty"`
}

func (o MieruOption) Equal(config C.InboundConfig) bool {
	return optionToString(o) == optionToString(config)
}

func NewMieru(*MieruOption) (*Mieru, error) {
	return nil, fmt.Errorf("mieru support is disabled by \"no_mieru\" build tag")
}
