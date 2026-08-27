# ARM64 BLAKE2b-256

This package keeps `golang.org/x/crypto/blake2b` on non-ARM64 and `purego`
builds. On ARM64 it implements the same unkeyed BLAKE2b-256 checksum with a
pure Go ABI NEON compression function.

The vector lane layout, diagonalization, and message schedule are derived from
the official BLAKE2 NEON implementation at commit
`ed1974ea83433eba7b2d95c5dcd9ac33cb847913`, used under CC0 1.0 Universal:

https://github.com/BLAKE2/BLAKE2/tree/ed1974ea83433eba7b2d95c5dcd9ac33cb847913/neon

There is no CGo path or runtime CPU dispatch. Advanced SIMD is part of the
AArch64 baseline architecture. Tests compare every input length from 0 through
4096 bytes with `golang.org/x/crypto/blake2b` and include fixed known-answer
vectors.
