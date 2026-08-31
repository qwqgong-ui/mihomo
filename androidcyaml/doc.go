// Package androidcyaml is the integration seam between mihomo and
// qwqgong-ui/AndroidCyaml.
//
// The contract is one-directional. AndroidCyaml owns everything Android:
// VpnService, JNI, WebView, the TUN contract, socket protection, and the policy
// that decides when any of the verbs below are called. This package owns the
// mapping from those verbs onto mihomo's internals, and nothing else. No
// Android type, no platform decision, and no AndroidCyaml policy belongs here.
//
// It exists because the alternative was worse. The AndroidCyaml wrapper used to
// import fourteen mihomo packages directly -- dialer, resolver, iface, geodata,
// statistic, hub, executor, route, dns, config, constant, listener/config, log
// and process -- so every upstream rename in any of them broke a downstream
// repository that has no way to see it coming. Routing the calls through one
// package means an upstream sync breaks this file instead, next to the code
// that knows what the call was for.
//
// When synchronizing MetaCubeX/mihomo:Alpha into dev:
//
//   - preserve this directory and the neutral extension points it calls;
//   - keep ordinary mihomo behavior unchanged when nothing is registered;
//   - if a signature here has to change, raise FacadeVersion so the AndroidCyaml
//     build fails loudly at compile time instead of at runtime on a device;
//   - verify both mihomo's tests and the AndroidCyaml native build.
package androidcyaml

// FacadeVersion is the contract version of this package. AndroidCyaml asserts it
// at build time, so a facade that moved under a consumer pinned to an older
// shape fails the build rather than producing a core that misbehaves on device.
//
// Raise it whenever an exported name here is added, removed, or changes meaning.
const FacadeVersion = 3
