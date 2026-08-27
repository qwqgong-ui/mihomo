//go:build !arm64 || purego

package blake2b

import xblake2b "golang.org/x/crypto/blake2b"

func Sum256(data []byte) [32]byte {
	return xblake2b.Sum256(data)
}
