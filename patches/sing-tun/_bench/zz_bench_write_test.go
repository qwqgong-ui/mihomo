package tun

import (
	"fmt"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/metacubex/sing-tun/internal/gtcpip/checksum"
	"github.com/metacubex/sing-tun/internal/gtcpip/header"
	"golang.org/x/sys/unix"
)

// benchTun backs NativeTun with a SOCK_DGRAM socketpair so that every
// tunFile.Write() lands as one countable datagram, which is exactly the
// write(2) count we care about.
func devNullTun(tb testing.TB) (*NativeTun, func()) {
	file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		tb.Skip(err)
	}
	tun := &NativeTun{
		tunFile:      file,
		vnetHdr:      true,
		tcpGROTable:  newTCPGROTable(),
		udpGROTable:  newUDPGROTable(),
		pendingWrite: make([][]byte, 0, idealBatchSize),
		pendingSpare: make([][]byte, 0, idealBatchSize),
	}
	return tun, func() { file.Close() }
}

func benchTun(b testing.TB) (*NativeTun, func() int64) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		b.Skip("socketpair: ", err)
	}
	_ = unix.SetsockoptInt(fds[0], unix.SOL_SOCKET, unix.SO_SNDBUF, 8<<20)
	_ = unix.SetsockoptInt(fds[1], unix.SOL_SOCKET, unix.SO_RCVBUF, 8<<20)
	var datagrams atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		scratch := make([]byte, 70000)
		for {
			n, err := unix.Read(fds[1], scratch)
			if err != nil || n <= 0 { // zero length sentinel ends the run
				return
			}
			datagrams.Add(1)
		}
	}()
	tun := &NativeTun{
		tunFd:        fds[0],
		tunFile:      os.NewFile(uintptr(fds[0]), "tun"),
		vnetHdr:      true,
		tcpGROTable:  newTCPGROTable(),
		udpGROTable:  newUDPGROTable(),
		pendingWrite: make([][]byte, 0, idealBatchSize),
		pendingSpare: make([][]byte, 0, idealBatchSize),
	}
	return tun, func() int64 {
		_, _ = unix.Write(fds[0], nil)
		<-done
		tun.tunFile.Close()
		unix.Close(fds[1])
		return datagrams.Load()
	}
}

func udpReply(flow int, payloadLen int) []byte {
	total := header.IPv4MinimumSize + header.UDPMinimumSize + payloadLen
	raw := make([]byte, virtioNetHdrLen+total, virtioNetHdrLen+65536)
	packet := raw[virtioNetHdrLen:]
	ipHdr := header.IPv4(packet)
	ipHdr.Encode(&header.IPv4Fields{
		TotalLength: uint16(total),
		Protocol:    uint8(header.UDPProtocolNumber),
		SrcAddr:     netip.MustParseAddr("1.1.1.1"),
		DstAddr:     netip.MustParseAddr("172.19.0.2"),
	})
	ipHdr.SetChecksum(^ipHdr.CalculateChecksum())
	udpHdr := header.UDP(ipHdr.Payload())
	udpHdr.SetSourcePort(443)
	udpHdr.SetDestinationPort(uint16(30000 + flow))
	udpHdr.SetLength(uint16(header.UDPMinimumSize + payloadLen))
	udpHdr.SetChecksum(^checksum.Checksum(udpHdr.Payload(), udpHdr.CalculateChecksum(
		header.PseudoHeaderChecksum(header.UDPProtocolNumber, ipHdr.SourceAddressSlice(), ipHdr.DestinationAddressSlice(), ipHdr.PayloadLength()))))
	return raw
}

// writers goroutines, each on its own UDP flow unless sameFlow is set.
func benchmarkWritePath(b *testing.B, patched bool, writers int, sameFlow bool) {
	tun, cleanup := devNullTun(b)
	defer cleanup()
	const payloadLen = 1200
	perWriter := b.N / writers
	if perWriter < 1 {
		perWriter = 1
	}
	b.SetBytes(int64(payloadLen))
	b.ResetTimer()
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			flow := w
			if sameFlow {
				flow = 0
			}
			packet := udpReply(flow, payloadLen)
			scratch := make([]byte, len(packet), cap(packet))
			for i := 0; i < perWriter; i++ {
				scratch = scratch[:len(packet)]
				copy(scratch, packet)
				if patched {
					_, _ = tun.Write(scratch)
				} else { // pre-patch Write(): one BatchWrite per packet
					_, _ = tun.BatchWrite([][]byte{scratch}, virtioNetHdrLen)
				}
			}
		}(w)
	}
	wg.Wait()
	b.StopTimer()
}

// TestWriteSyscallCount counts the actual write(2) calls each variant issues,
// which is what the coalescing is meant to reduce.
func TestWriteSyscallCount(t *testing.T) {
	const packets = 20000
	for _, sameFlow := range []bool{false, true} {
		for _, writers := range []int{1, 8, 64} {
			for _, patched := range []bool{false, true} {
				tun, finish := benchTun(t)
				var wg sync.WaitGroup
				for w := 0; w < writers; w++ {
					wg.Add(1)
					go func(w int) {
						defer wg.Done()
						flow := w
						if sameFlow {
							flow = 0
						}
						packet := udpReply(flow, 1200)
						scratch := make([]byte, len(packet), cap(packet))
						for i := 0; i < packets/writers; i++ {
							scratch = scratch[:len(packet)]
							copy(scratch, packet)
							if patched {
								_, _ = tun.Write(scratch)
							} else {
								_, _ = tun.BatchWrite([][]byte{scratch}, virtioNetHdrLen)
							}
						}
					}(w)
				}
				wg.Wait()
				sent := int64(packets / writers * writers)
				calls := finish()
				name := "distinct-flows"
				if sameFlow {
					name = "same-flow     "
				}
				variant := "baseline"
				if patched {
					variant = "patched "
				}
				t.Logf("%s %s writers=%-3d packets=%d write(2)=%-6d ratio=%.3f",
					variant, name, writers, sent, calls, float64(calls)/float64(sent))
			}
		}
	}
}

func BenchmarkWriteBaseline(b *testing.B) {
	for _, w := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("flows=%d", w), func(b *testing.B) { benchmarkWritePath(b, false, w, false) })
	}
	b.Run("sameflow=8", func(b *testing.B) { benchmarkWritePath(b, false, 8, true) })
}

func BenchmarkWritePatched(b *testing.B) {
	for _, w := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("flows=%d", w), func(b *testing.B) { benchmarkWritePath(b, true, w, false) })
	}
	b.Run("sameflow=8", func(b *testing.B) { benchmarkWritePath(b, true, 8, true) })
}
