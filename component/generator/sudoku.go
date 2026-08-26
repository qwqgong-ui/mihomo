//go:build !no_sudoku

package generator

import "github.com/metacubex/mihomo/transport/sudoku"

func genSudokuKeyPair() (string, string, error) {
	return sudoku.GenKeyPair()
}
