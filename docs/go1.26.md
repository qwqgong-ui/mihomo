# Go 1.26 and module notes

## Release policy

The Alpha branch is built and tested only with the MetaCubeX Go 1.26 toolchain. Both CI and release builds set `GOEXPERIMENT=simd`; older Go toolchains are not supported by this branch.

AMD64 artifacts are built only with `GOAMD64=v3`. In addition to the v2 baseline, v3 requires AVX, AVX2, BMI1, BMI2, FMA, LZCNT, MOVBE, and OSXSAVE support; v1/v2-compatible AMD64 packages are not published.

The custom toolchain is downloaded from the MetaCubeX Go build release used by CI and release builds.

## Experimental SIMD

The experimental `simd` package offers portable, vector-size-agnostic operations and uses hardware vector instructions where supported. The related `simd/archsimd` package exposes architecture-specific operations. Both APIs require `GOEXPERIMENT=simd` and remain unstable.

Enabling the experiment makes the API available to Mihomo and its dependencies; it does not by itself accelerate code that does not call the SIMD API.

## Module update policy

All direct and indirect Go modules are checked automatically. Updates are grouped into a single pull request and must pass the Go 1.26 SIMD test matrix before they are merged.

`golang.org/x/net` is temporarily pinned to `v0.53.0`. Versions starting at `v0.54.0` use the new HTTP/2 transport wrapper and currently trigger a nil-pointer panic in the `sing-mux v0.3.10` HTTP/2 mux path. Related `golang.org/x/*` modules and HTTP/2 helpers are kept at the newest versions that do not force `x/net` past that boundary. These pins can be removed together after the integration is fixed upstream.

## What the modules contain

- Protocols and multiplexing: `sing-*`, `smux`, `kcp-go`, `mieru`, VMess, Shadowsocks, QUIC, SSH, RESTLS, Tailscale, and WireGuard implementations.
- Network and system integration: DNS, DHCP, netlink, iptables, packet capture, TUN/gVisor, socket helpers, file watching, TFO, and CPU/runtime tuning.
- Cryptography and TLS: `utls`, TLS, ML-KEM, HPKE, ChaCha, BLAKE3, Ed25519, AES-CCM, Age, and related primitives.
- Data, configuration, and compression: YAML, MessagePack, CBOR, UUID, regular expressions, protobuf, Brotli, Snappy, LZO, LZ4, XZ, and 7-Zip support.
- Utilities and tests: logging, generic collections, load balancing, assertions, comparison helpers, profiling, and Go analysis tools.
- Indirect modules: transitive implementation details required by the direct modules. They are tracked in `go.mod` and `go.sum` so builds remain reproducible.
