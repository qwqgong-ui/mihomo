//go:build no_sudoku

package inbound

import (
	"fmt"

	C "github.com/metacubex/mihomo/constant"
)

type Sudoku struct {
	*Base
}

type SudokuOption struct {
	BaseOption
	Key                    string                 `inbound:"key"`
	AEADMethod             string                 `inbound:"aead-method,omitempty"`
	PaddingMin             *int                   `inbound:"padding-min,omitempty"`
	PaddingMax             *int                   `inbound:"padding-max,omitempty"`
	TableType              string                 `inbound:"table-type,omitempty"`
	HandshakeTimeoutSecond *int                   `inbound:"handshake-timeout,omitempty"`
	EnablePureDownlink     *bool                  `inbound:"enable-pure-downlink,omitempty"`
	CustomTable            string                 `inbound:"custom-table,omitempty"`
	CustomTables           []string               `inbound:"custom-tables,omitempty"`
	DisableHTTPMask        bool                   `inbound:"disable-http-mask,omitempty"`
	HTTPMaskMode           string                 `inbound:"http-mask-mode,omitempty"`
	PathRoot               string                 `inbound:"path-root,omitempty"`
	Fallback               string                 `inbound:"fallback,omitempty"`
	HTTPMaskOptions        *SudokuHTTPMaskOptions `inbound:"httpmask,omitempty"`
	MuxOption              MuxOption              `inbound:"mux-option,omitempty"`
}

type SudokuHTTPMaskOptions struct {
	Disable  bool   `inbound:"disable,omitempty"`
	Mode     string `inbound:"mode,omitempty"`
	PathRoot string `inbound:"path-root,omitempty"`
}

func (o SudokuOption) Equal(config C.InboundConfig) bool {
	return optionToString(o) == optionToString(config)
}

func NewSudoku(*SudokuOption) (*Sudoku, error) {
	return nil, fmt.Errorf("sudoku support is disabled by \"no_sudoku\" build tag")
}
