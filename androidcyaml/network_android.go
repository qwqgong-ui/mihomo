//go:build android

package androidcyaml

import "github.com/metacubex/mihomo/dns"

// UpdateSystemDNS installs the resolvers the current physical network handed
// out, so `system://` means what Android says it means.
//
// Android has no readable /etc/resolv.conf, so without this the core has no
// system DNS at all. AndroidCyaml reads them from the LinkProperties of the
// network Android itself scored best, and pushes them again on every handover.
func UpdateSystemDNS(servers []string) {
	dns.UpdateSystemDNS(servers)
}
