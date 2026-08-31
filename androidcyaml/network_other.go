//go:build !android

package androidcyaml

// UpdateSystemDNS is Android-only: mihomo discovers system resolvers from the
// platform everywhere else. The stub keeps this package building on the host so
// mihomo's own tests and vet runs are not split by GOOS.
func UpdateSystemDNS([]string) {}
