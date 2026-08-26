//go:build no_sudoku

package generator

import "errors"

func genSudokuKeyPair() (string, string, error) {
	return "", "", errors.New("sudoku support is disabled by \"no_sudoku\" build tag")
}
