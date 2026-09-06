package dns

// export functions from tunnel module

import "github.com/metacubex/mihomo/tunnel"

const RespectRules = tunnel.DnsRespectRules

type dnsDialer = tunnel.DNSDialer

var newDNSDialer = tunnel.NewDNSDialer
var newSystemDNSDialer = tunnel.NewSystemDNSDialer

// dialTunnelDNS is a variable so a test can stand in for node selection.
var dialTunnelDNS = tunnel.DialTunnelDNS
