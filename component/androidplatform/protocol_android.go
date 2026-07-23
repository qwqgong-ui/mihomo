//go:build android

package androidplatform

type platformRequest struct {
	Operation          string   `json:"operation"`
	Tun                *tunSpec `json:"tun,omitempty"`
	Network            string   `json:"network,omitempty"`
	SourceAddress      string   `json:"sourceAddress,omitempty"`
	SourcePort         uint16   `json:"sourcePort,omitempty"`
	DestinationAddress string   `json:"destinationAddress,omitempty"`
	DestinationPort    uint16   `json:"destinationPort,omitempty"`
}

type platformResponse struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
	UID         int64  `json:"uid,omitempty"`
	PackageName string `json:"packageName,omitempty"`
}

type tunSpec struct {
	MTU                      uint32   `json:"mtu"`
	Inet4Address             []string `json:"inet4Address"`
	Inet6Address             []string `json:"inet6Address"`
	AutoRoute                bool     `json:"autoRoute"`
	Inet4RouteAddress        []string `json:"inet4RouteAddress"`
	Inet6RouteAddress        []string `json:"inet6RouteAddress"`
	Inet4RouteExcludeAddress []string `json:"inet4RouteExcludeAddress"`
	Inet6RouteExcludeAddress []string `json:"inet6RouteExcludeAddress"`
	DNSServerAddress         []string `json:"dnsServerAddress"`
	IncludePackage           []string `json:"includePackage"`
	ExcludePackage           []string `json:"excludePackage"`
}
