//go:build no_sudoku

package outbound

import "fmt"

type Sudoku struct {
	*Base
}

type SudokuOption struct {
	BasicOption
	Name               string                 `proxy:"name"`
	Server             string                 `proxy:"server"`
	Port               int                    `proxy:"port"`
	Key                string                 `proxy:"key"`
	AEADMethod         string                 `proxy:"aead-method,omitempty"`
	PaddingMin         *int                   `proxy:"padding-min,omitempty"`
	PaddingMax         *int                   `proxy:"padding-max,omitempty"`
	TableType          string                 `proxy:"table-type,omitempty"`
	EnablePureDownlink *bool                  `proxy:"enable-pure-downlink,omitempty"`
	HTTPMask           *bool                  `proxy:"http-mask,omitempty"`
	HTTPMaskMode       string                 `proxy:"http-mask-mode,omitempty"`
	HTTPMaskTLS        bool                   `proxy:"http-mask-tls,omitempty"`
	HTTPMaskHost       string                 `proxy:"http-mask-host,omitempty"`
	PathRoot           string                 `proxy:"path-root,omitempty"`
	Multiplex          string                 `proxy:"multiplex,omitempty"`
	HTTPMaskMultiplex  string                 `proxy:"http-mask-multiplex,omitempty"`
	HTTPMaskOptions    *SudokuHTTPMaskOptions `proxy:"httpmask,omitempty"`
	CustomTable        string                 `proxy:"custom-table,omitempty"`
	CustomTables       []string               `proxy:"custom-tables,omitempty"`
}

type SudokuHTTPMaskOptions struct {
	Disable   bool   `proxy:"disable,omitempty"`
	Mode      string `proxy:"mode,omitempty"`
	TLS       bool   `proxy:"tls,omitempty"`
	Host      string `proxy:"host,omitempty"`
	PathRoot  string `proxy:"path-root,omitempty"`
	Multiplex string `proxy:"multiplex,omitempty"`
}

func NewSudoku(SudokuOption) (*Sudoku, error) {
	return nil, fmt.Errorf("sudoku support is disabled by \"no_sudoku\" build tag")
}
