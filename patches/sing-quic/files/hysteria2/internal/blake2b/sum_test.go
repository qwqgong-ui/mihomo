package blake2b

import (
	"bytes"
	"encoding/hex"
	"math/rand"
	"strconv"
	"testing"

	xblake2b "golang.org/x/crypto/blake2b"
)

func TestSum256KnownAnswer(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "0e5751c026e543b2e8ab2eb06099daa1d1e5df47778f7787faab45cdf12fe3a8"},
		{"abc", "bddd813c634239723171ef3fee98579b94964e3bb1cb3e427262c8c068d52319"},
	}
	for _, test := range tests {
		got := Sum256([]byte(test.input))
		if hex.EncodeToString(got[:]) != test.want {
			t.Fatalf("Sum256(%q) = %x, want %s", test.input, got, test.want)
		}
	}
}

func TestSum256MatchesXCrypto(t *testing.T) {
	for n := 0; n <= 4096; n++ {
		offset := n & 15
		storage := make([]byte, n+offset)
		input := storage[offset:]
		_, _ = rand.New(rand.NewSource(int64(n))).Read(input)
		got := Sum256(input)
		want := xblake2b.Sum256(input)
		if !bytes.Equal(got[:], want[:]) {
			t.Fatalf("length %d: got %x, want %x", n, got, want)
		}
	}
}

func BenchmarkSum256(b *testing.B) {
	for _, size := range []int{16, 32, 64, 128, 300, 1200} {
		input := make([]byte, size)
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			b.Run("local", func(b *testing.B) {
				b.SetBytes(int64(size))
				for i := 0; i < b.N; i++ {
					_ = Sum256(input)
				}
			})
			b.Run("xcrypto", func(b *testing.B) {
				b.SetBytes(int64(size))
				for i := 0; i < b.N; i++ {
					_ = xblake2b.Sum256(input)
				}
			})
		})
	}
}
