//go:build arm64 && !purego

package blake2b

import "encoding/binary"

const blockSize = 128

var initialState = [8]uint64{
	0x6a09e667f3bcc908, 0xbb67ae8584caa73b,
	0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
	0x510e527fade682d1, 0x9b05688c2b3e6c1f,
	0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
}

func Sum256(data []byte) [32]byte {
	h := initialState
	h[0] ^= uint64(32) | (1 << 16) | (1 << 24)
	var counter [2]uint64

	if length := len(data); length > blockSize {
		n := length &^ (blockSize - 1)
		if length == n {
			n -= blockSize
		}
		hashBlocks(&h, &counter, 0, data[:n])
		data = data[n:]
	}

	var block [blockSize]byte
	offset := copy(block[:], data)
	remaining := uint64(blockSize - offset)
	if counter[0] < remaining {
		counter[1]--
	}
	counter[0] -= remaining
	hashBlocks(&h, &counter, ^uint64(0), block[:])

	var sum [32]byte
	for i, value := range h[:4] {
		binary.LittleEndian.PutUint64(sum[8*i:], value)
	}
	return sum
}

//go:noescape
func hashBlocks(h *[8]uint64, counter *[2]uint64, flag uint64, blocks []byte)
