package tun

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/metacubex/sing/common/buf"
	M "github.com/metacubex/sing/common/metadata"
	N "github.com/metacubex/sing/common/network"

	"github.com/metacubex/sing-tun/internal/gtcpip/checksum"
	"github.com/metacubex/sing-tun/internal/gtcpip/header"
)

// ---------- 1. TCP checksum: full recompute (baseline) vs incremental ----------

func benchPacket4(payloadLen int) (header.IPv4, header.TCP) {
	packet := make([]byte, header.IPv4MinimumSize+header.TCPMinimumSize+payloadLen)
	for i := header.IPv4MinimumSize + header.TCPMinimumSize; i < len(packet); i++ {
		packet[i] = byte(i)
	}
	ipHdr := header.IPv4(packet)
	ipHdr.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(packet)),
		Protocol:    uint8(header.TCPProtocolNumber),
		SrcAddr:     netip.MustParseAddr("192.168.1.2"),
		DstAddr:     netip.MustParseAddr("1.1.1.1"),
	})
	tcpHdr := header.TCP(ipHdr.Payload())
	tcpHdr.Encode(&header.TCPFields{
		SrcPort: 51234, DstPort: 443,
		DataOffset: header.TCPMinimumSize, Flags: header.TCPFlagAck, WindowSize: 65535,
	})
	tcpHdr.SetChecksum(^checksum.Checksum(tcpHdr.Payload(), tcpHdr.CalculateChecksum(
		header.PseudoHeaderChecksum(header.TCPProtocolNumber, ipHdr.SourceAddressSlice(), ipHdr.DestinationAddressSlice(), ipHdr.PayloadLength()))))
	return ipHdr, tcpHdr
}

func BenchmarkChecksumBaselineFull(b *testing.B) {
	for _, size := range []int{64, 1400, 16384, 65495} {
		ipHdr, tcpHdr := benchPacket4(size)
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.SetBytes(int64(len(ipHdr)))
			for i := 0; i < b.N; i++ {
				tcpHdr.SetChecksum(^checksum.Checksum(tcpHdr.Payload(), tcpHdr.CalculateChecksum(
					header.PseudoHeaderChecksum(header.TCPProtocolNumber, ipHdr.SourceAddressSlice(), ipHdr.DestinationAddressSlice(), ipHdr.PayloadLength()))))
			}
		})
	}
}

func BenchmarkChecksumPatchedIncremental(b *testing.B) {
	oldSrc := netip.MustParseAddr("192.168.1.2")
	newSrc := netip.MustParseAddr("172.19.0.2")
	oldDst := netip.MustParseAddr("1.1.1.1")
	newDst := netip.MustParseAddr("172.19.0.1")
	for _, size := range []int{64, 1400, 16384, 65495} {
		ipHdr, tcpHdr := benchPacket4(size)
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.SetBytes(int64(len(ipHdr)))
			for i := 0; i < b.N; i++ {
				var delta checksumDelta
				delta.addAddr4(oldSrc, newSrc)
				delta.addAddr4(oldDst, newDst)
				delta.addUint16(51234, 10001)
				delta.addUint16(443, 39999)
				tcpHdr.SetChecksum(delta.apply(tcpHdr.Checksum()))
			}
		})
	}
}

// ---------- 2. TCP NAT reverse lookup: old map+mutex vs new atomic array ----------

type legacyTCPSession struct {
	sync.Mutex
	Source      netip.AddrPort
	Destination netip.AddrPort
	LastActive  time.Time
}

type legacyTCPNat struct {
	portIndex  uint16
	portAccess sync.RWMutex
	addrAccess sync.RWMutex
	addrMap    map[tcpNatKey]uint16
	portMap    map[uint16]*legacyTCPSession
}

func newLegacyNat() *legacyTCPNat {
	return &legacyTCPNat{portIndex: 10000, addrMap: make(map[tcpNatKey]uint16), portMap: make(map[uint16]*legacyTCPSession)}
}

func (n *legacyTCPNat) LookupBack(port uint16) *legacyTCPSession {
	n.portAccess.RLock()
	session := n.portMap[port]
	n.portAccess.RUnlock()
	if session != nil {
		session.Lock()
		if time.Since(session.LastActive) > time.Second {
			session.LastActive = time.Now()
		}
		session.Unlock()
	}
	return session
}

func (n *legacyTCPNat) Lookup(source netip.AddrPort, destination netip.AddrPort) uint16 {
	key := tcpNatKey{Source: source, Destination: destination}
	n.addrAccess.RLock()
	port, loaded := n.addrMap[key]
	n.addrAccess.RUnlock()
	if loaded {
		return port
	}
	n.addrAccess.Lock()
	nextPort := n.portIndex
	n.portIndex++
	n.addrMap[key] = nextPort
	n.addrAccess.Unlock()
	n.portAccess.Lock()
	n.portMap[nextPort] = &legacyTCPSession{Source: source, Destination: destination, LastActive: time.Now()}
	n.portAccess.Unlock()
	return nextPort
}

func natSessions(count int) []netip.AddrPort {
	out := make([]netip.AddrPort, count)
	for i := range out {
		out[i] = netip.AddrPortFrom(netip.MustParseAddr("192.168.1.2"), uint16(20000+i))
	}
	return out
}

func BenchmarkNatLookupBackBaseline(b *testing.B) {
	const sessions = 2000
	nat := newLegacyNat()
	dst := netip.AddrPortFrom(netip.MustParseAddr("1.1.1.1"), 443)
	ports := make([]uint16, sessions)
	for i, src := range natSessions(sessions) {
		ports[i] = nat.Lookup(src, dst)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if nat.LookupBack(ports[i%sessions]) == nil {
			b.Fatal("missing session")
		}
	}
}

func BenchmarkNatLookupBackPatched(b *testing.B) {
	const sessions = 2000
	nat := NewNat(context.Background(), time.Minute)
	dst := netip.AddrPortFrom(netip.MustParseAddr("1.1.1.1"), 443)
	ports := make([]uint16, sessions)
	for i, src := range natSessions(sessions) {
		port, err := nat.Lookup(src, dst, noopHandler{})
		if err != nil {
			b.Fatal(err)
		}
		ports[i] = port
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if nat.LookupBack(ports[i%sessions]) == nil {
			b.Fatal("missing session")
		}
	}
}

type noopHandler struct{}

func (noopHandler) PrepareConnection(network string, source M.Socksaddr, destination M.Socksaddr, routeContext DirectRouteContext, timeout time.Duration) (DirectRouteDestination, error) {
	return nil, nil
}
func (noopHandler) NewConnection(ctx context.Context, conn net.Conn, metadata M.Metadata) error {
	return nil
}
func (noopHandler) NewPacket(ctx context.Context, key netip.AddrPort, buffer *buf.Buffer, metadata M.Metadata, init func(natConn N.PacketConn) N.PacketWriter) {
}
func (noopHandler) NewError(ctx context.Context, err error) {}

// ---------- 3. write-side GRO: with and without the redundant checksum pass ----------

// groBatch builds a batch of same-flow MTU sized TCP segments the way the
// system stack hands them to BatchWrite: virtio headroom in front, valid
// checksums, consecutive sequence numbers.
func groBatch(count int, payloadLen int) [][]byte {
	bufs := make([][]byte, count)
	for i := range bufs {
		total := header.IPv4MinimumSize + header.TCPMinimumSize + payloadLen
		raw := make([]byte, virtioNetHdrLen+total, virtioNetHdrLen+65536)
		packet := raw[virtioNetHdrLen:]
		for j := header.IPv4MinimumSize + header.TCPMinimumSize; j < len(packet); j++ {
			packet[j] = byte(j + i)
		}
		ipHdr := header.IPv4(packet)
		ipHdr.Encode(&header.IPv4Fields{
			TotalLength: uint16(total),
			Protocol:    uint8(header.TCPProtocolNumber),
			SrcAddr:     netip.MustParseAddr("172.19.0.2"),
			DstAddr:     netip.MustParseAddr("172.19.0.1"),
		})
		ipHdr.SetChecksum(^ipHdr.CalculateChecksum())
		tcpHdr := header.TCP(ipHdr.Payload())
		tcpHdr.Encode(&header.TCPFields{
			SrcPort: 10001, DstPort: 39999,
			SeqNum:     uint32(1 + i*payloadLen),
			AckNum:     1,
			DataOffset: header.TCPMinimumSize, Flags: header.TCPFlagAck, WindowSize: 65535,
		})
		tcpHdr.SetChecksum(^checksum.Checksum(tcpHdr.Payload(), tcpHdr.CalculateChecksum(
			header.PseudoHeaderChecksum(header.TCPProtocolNumber, ipHdr.SourceAddressSlice(), ipHdr.DestinationAddressSlice(), ipHdr.PayloadLength()))))
		bufs[i] = raw
	}
	return bufs
}

func benchmarkGRO(b *testing.B, trustCSum bool) {
	const count = 32
	const payloadLen = 1400
	tcpTable := newTCPGROTable()
	udpTable := newUDPGROTable()
	var toWrite []int
	template := groBatch(count, payloadLen)
	bufs := make([][]byte, count)
	scratch := make([][]byte, count)
	for i := range scratch {
		scratch[i] = make([]byte, len(template[i]), cap(template[i]))
	}
	b.ReportAllocs()
	b.SetBytes(int64(count * (header.IPv4MinimumSize + header.TCPMinimumSize + payloadLen)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for j := range template { // handleGRO mutates the buffers, restore each round
			scratch[j] = scratch[j][:len(template[j])]
			copy(scratch[j], template[j])
			bufs[j] = scratch[j]
		}
		toWrite = toWrite[:0]
		tcpTable.reset()
		udpTable.reset()
		b.StartTimer()
		err := handleGRO(bufs, virtioNetHdrLen, tcpTable, udpTable, groDisablementFlags(0), trustCSum, &toWrite)
		if err != nil {
			b.Fatal(err)
		}
		if i == 0 {
			b.ReportMetric(float64(len(toWrite)), "writes/batch")
		}
	}
}

func BenchmarkGROBaselineValidate(b *testing.B) { benchmarkGRO(b, false) }
func BenchmarkGROPatchedTrusted(b *testing.B)   { benchmarkGRO(b, true) }
