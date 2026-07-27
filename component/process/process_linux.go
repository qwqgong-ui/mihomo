package process

import (
	"encoding/binary"
	"net/netip"
	"runtime"
	"strings"
	"unsafe"

	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

const (
	SOCK_DIAG_BY_FAMILY  = 20
	inetDiagRequestSize  = int(unsafe.Sizeof(inetDiagRequest{}))
	inetDiagResponseSize = int(unsafe.Sizeof(inetDiagResponse{}))
)

type inetDiagRequest struct {
	Family   byte
	Protocol byte
	Ext      byte
	Pad      byte
	States   uint32

	SrcPort [2]byte
	DstPort [2]byte
	Src     [16]byte
	Dst     [16]byte
	If      uint32
	Cookie  [2]uint32
}

type inetDiagResponse struct {
	Family  byte
	State   byte
	Timer   byte
	ReTrans byte

	SrcPort [2]byte
	DstPort [2]byte
	Src     [16]byte
	Dst     [16]byte
	If      uint32
	Cookie  [2]uint32

	Expires uint32
	RQueue  uint32
	WQueue  uint32
	UID     uint32
	INode   uint32
}

func findProcessName(network string, ip netip.Addr, srcPort int) (uint32, string, error) {
	if !ip.IsValid() || srcPort < 0 || srcPort > 65535 {
		return 0, "", ErrNotFound
	}
	return findProcessNameByAddr(network, netip.AddrPortFrom(ip.Unmap(), uint16(srcPort)), netip.AddrPort{})
}

func findProcessNameByAddr(network string, src, dst netip.AddrPort) (uint32, string, error) {
	if !src.IsValid() {
		return 0, "", ErrNotFound
	}
	src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
	if dst.IsValid() {
		dst = netip.AddrPortFrom(dst.Addr().Unmap(), dst.Port())
	}

	uid, inode, err := resolveSocketOwner(network, src, dst)
	if runtime.GOOS == "android" {
		uid, inode, err = resolveAndroidSocketOwner(network, src, uid, inode, err)
	}
	if err != nil || inode == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return 0, "", err
	}

	name, err := resolveProcessNameByProcSearch(inode, uid)
	if runtime.GOOS == "android" && err != nil && uid != 0 {
		name, err = resolveProcessNameByUID(uid)
	}
	return uid, name, err
}

func resolveSocketOwner(network string, src, dst netip.AddrPort) (uint32, uint32, error) {
	if canUseExactSocketLookup(src, dst) {
		uid, inode, err := resolveSocketByNetlinkExact(network, src, dst)
		if err == nil && inode != 0 {
			return uid, inode, nil
		}
	}
	return resolveSocketByNetlink(network, src.Addr(), int(src.Port()))
}

func resolveAndroidSocketOwner(network string, src netip.AddrPort, uid, inode uint32, err error) (uint32, uint32, error) {
	if err != nil {
		return resolveSocketByProcFS(network, src.Addr(), int(src.Port()))
	}
	if uid != 0 {
		return uid, inode, nil
	}
	procUID, procInode, procErr := resolveSocketByProcFS(network, src.Addr(), int(src.Port()))
	if procErr == nil && procUID != 0 {
		return procUID, procInode, nil
	}
	return uid, inode, err
}

func canUseExactSocketLookup(src, dst netip.AddrPort) bool {
	return src.IsValid() && src.Port() != 0 && dst.IsValid() && dst.Port() != 0 && src.Addr().BitLen() == dst.Addr().BitLen()
}

func resolveSocketByNetlinkExact(network string, src, dst netip.AddrPort) (uint32, uint32, error) {
	if !canUseExactSocketLookup(src, dst) {
		return 0, 0, ErrNotFound
	}

	request, isTCP, err := newInetDiagRequest(network, src.Addr())
	if err != nil {
		return 0, 0, err
	}
	if isTCP {
		setInetDiagEndpoints(request, src, dst)
	} else {
		// udp_diag_dump_one expects the incoming packet direction (remote to
		// local), historically reversed from inet_diag response fields.
		setInetDiagEndpoints(request, dst, src)
	}

	messages, err := executeInetDiag(request, netlink.Request)
	if err != nil {
		return 0, 0, err
	}
	for _, msg := range messages {
		response, ok := parseInetDiagResponse(msg.Data)
		if !ok || !matchesExactSocket(response, src, dst, isTCP) {
			continue
		}
		return response.UID, response.INode, nil
	}
	return 0, 0, ErrNotFound
}

func resolveSocketByNetlink(network string, ip netip.Addr, srcPort int) (uint32, uint32, error) {
	request, _, err := newInetDiagRequest(network, ip)
	if err != nil {
		return 0, 0, err
	}
	copy(request.Src[:], ip.AsSlice())
	binary.BigEndian.PutUint16(request.SrcPort[:], uint16(srcPort))

	messages, err := executeInetDiag(request, netlink.Request|netlink.Dump)
	if err != nil {
		return 0, 0, err
	}

	wanted := netip.AddrPortFrom(ip.Unmap(), uint16(srcPort))
	for _, msg := range messages {
		response, ok := parseInetDiagResponse(msg.Data)
		if !ok {
			continue
		}
		local, ok := inetDiagAddrPort(response.Family, response.Src, response.SrcPort)
		if !ok || local != wanted {
			continue
		}
		return response.UID, response.INode, nil
	}
	return 0, 0, ErrNotFound
}

func newInetDiagRequest(network string, addr netip.Addr) (*inetDiagRequest, bool, error) {
	request := &inetDiagRequest{
		States: 0xffffffff,
		Cookie: [2]uint32{0xffffffff, 0xffffffff},
	}

	switch {
	case addr.Is4():
		request.Family = unix.AF_INET
	case addr.Is6():
		request.Family = unix.AF_INET6
	default:
		return nil, false, ErrNotFound
	}

	switch {
	case strings.HasPrefix(network, "tcp"):
		request.Protocol = unix.IPPROTO_TCP
		return request, true, nil
	case strings.HasPrefix(network, "udp"):
		request.Protocol = unix.IPPROTO_UDP
		return request, false, nil
	default:
		return nil, false, ErrInvalidNetwork
	}
}

func matchesExactSocket(response *inetDiagResponse, src, dst netip.AddrPort, isTCP bool) bool {
	local, ok := inetDiagAddrPort(response.Family, response.Src, response.SrcPort)
	if !ok || local.Port() != src.Port() {
		return false
	}
	localIP := local.Addr().Unmap()
	if localIP != src.Addr() && (isTCP || !localIP.IsUnspecified()) {
		return false
	}

	remote, ok := inetDiagAddrPort(response.Family, response.Dst, response.DstPort)
	if !ok {
		return false
	}
	if isTCP {
		return remote == dst
	}
	return remote.Port() == 0 || remote == dst
}

func parseInetDiagResponse(data []byte) (*inetDiagResponse, bool) {
	if len(data) < inetDiagResponseSize {
		return nil, false
	}
	response := (*inetDiagResponse)(unsafe.Pointer(&data[0]))
	return response, response.INode != 0
}

func setInetDiagEndpoints(request *inetDiagRequest, src, dst netip.AddrPort) {
	copy(request.Src[:], src.Addr().AsSlice())
	copy(request.Dst[:], dst.Addr().AsSlice())
	binary.BigEndian.PutUint16(request.SrcPort[:], src.Port())
	binary.BigEndian.PutUint16(request.DstPort[:], dst.Port())
}

func inetDiagAddrPort(family byte, rawIP [16]byte, rawPort [2]byte) (netip.AddrPort, bool) {
	var ip netip.Addr
	switch family {
	case unix.AF_INET:
		var addr [4]byte
		copy(addr[:], rawIP[:4])
		ip = netip.AddrFrom4(addr)
	case unix.AF_INET6:
		ip = netip.AddrFrom16(rawIP).Unmap()
	default:
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(ip, binary.BigEndian.Uint16(rawPort[:])), true
}

func executeInetDiag(request *inetDiagRequest, flags netlink.HeaderFlags) ([]netlink.Message, error) {
	conn, err := netlink.Dial(unix.NETLINK_INET_DIAG, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	return conn.Execute(netlink.Message{
		Header: netlink.Header{
			Type:  SOCK_DIAG_BY_FAMILY,
			Flags: flags,
		},
		Data: (*(*[inetDiagRequestSize]byte)(unsafe.Pointer(request)))[:],
	})
}
