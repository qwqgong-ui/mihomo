package process

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode"
	"unsafe"

	"golang.org/x/sys/unix"
)

const recentPIDLimit = 8

type recentPIDs struct {
	mu   sync.RWMutex
	pids [recentPIDLimit]string
	len  int
}

var recentProcessPIDs sync.Map // map[uint32]*recentPIDs

func resolveProcessNameByProcSearch(inode, uid uint32) (string, error) {
	buffer := make([]byte, unix.PathMax)
	socket := fmt.Appendf(nil, "socket:[%d]", inode)

	// Most applications create many connections from the same process. Try the
	// recently successful PIDs for this UID before scanning every /proc entry.
	// Each candidate is still verified against the current socket inode, so PID
	// reuse and stale cache entries cannot produce a false match.
	recentPIDs := loadRecentProcessPIDs(uid)
	for _, pid := range recentPIDs {
		processPath := filepath.Join("/proc", pid)
		info, err := os.Stat(processPath)
		if err != nil || !hasProcessUID(info, uid) {
			continue
		}
		name, matched, err := findProcessNameInPath(processPath, socket, buffer)
		if err != nil {
			if isTransientProcError(err) {
				continue
			}
			return "", err
		}
		if matched {
			rememberProcessPID(uid, pid)
			return name, nil
		}
	}

	files, err := os.ReadDir("/proc")
	if err != nil {
		return "", err
	}
	for _, file := range files {
		if !file.IsDir() || !isPid(file.Name()) || containsPID(recentPIDs, file.Name()) {
			continue
		}
		info, err := file.Info()
		if err != nil || !hasProcessUID(info, uid) {
			continue
		}

		processPath := filepath.Join("/proc", file.Name())
		name, matched, err := findProcessNameInPath(processPath, socket, buffer)
		if err != nil {
			if isTransientProcError(err) {
				continue
			}
			return "", err
		}
		if matched {
			rememberProcessPID(uid, file.Name())
			return name, nil
		}
	}

	return "", fmt.Errorf("process of uid(%d),inode(%d) not found", uid, inode)
}

func findProcessNameInPath(processPath string, socket, buffer []byte) (string, bool, error) {
	fds, err := os.ReadDir(filepath.Join(processPath, "fd"))
	if err != nil {
		return "", false, nil
	}
	for _, fd := range fds {
		n, err := unix.Readlink(filepath.Join(processPath, "fd", fd.Name()), buffer)
		if err != nil || !bytes.Equal(buffer[:n], socket) {
			continue
		}
		name, err := readProcessName(processPath)
		return name, err == nil, err
	}
	return "", false, nil
}

func readProcessName(processPath string) (string, error) {
	if runtime.GOOS == "android" {
		cmdline, err := os.ReadFile(path.Join(processPath, "cmdline"))
		if err != nil {
			return "", err
		}
		return splitCmdline(cmdline), nil
	}
	return os.Readlink(filepath.Join(processPath, "exe"))
}

func resolveProcessNameByUID(uid uint32) (string, error) {
	files, err := os.ReadDir("/proc")
	if err != nil {
		return "", err
	}
	for _, file := range files {
		if !file.IsDir() || !isPid(file.Name()) {
			continue
		}
		info, err := file.Info()
		if err != nil || !hasProcessUID(info, uid) {
			continue
		}
		name, err := readProcessName(filepath.Join("/proc", file.Name()))
		if err == nil && name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("no process found with uid %d", uid)
}

func hasProcessUID(info os.FileInfo, uid uint32) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uid
}

func isTransientProcError(err error) bool {
	return os.IsNotExist(err) || os.IsPermission(err)
}

func containsPID(pids []string, pid string) bool {
	for _, candidate := range pids {
		if candidate == pid {
			return true
		}
	}
	return false
}

func loadRecentProcessPIDs(uid uint32) []string {
	value, ok := recentProcessPIDs.Load(uid)
	if !ok {
		return nil
	}
	recent := value.(*recentPIDs)
	recent.mu.RLock()
	defer recent.mu.RUnlock()
	pids := make([]string, recent.len)
	copy(pids, recent.pids[:recent.len])
	return pids
}

func rememberProcessPID(uid uint32, pid string) {
	value, _ := recentProcessPIDs.LoadOrStore(uid, &recentPIDs{})
	recent := value.(*recentPIDs)
	recent.mu.Lock()
	defer recent.mu.Unlock()

	index := recent.len
	for i := 0; i < recent.len; i++ {
		if recent.pids[i] == pid {
			index = i
			break
		}
	}
	if index == 0 && recent.len > 0 {
		return
	}
	if index == recent.len && recent.len < recentPIDLimit {
		recent.len++
	}
	if index >= recentPIDLimit {
		index = recentPIDLimit - 1
	}
	copy(recent.pids[1:index+1], recent.pids[:index])
	recent.pids[0] = pid
}

func splitCmdline(cmdline []byte) string {
	cmdline = bytes.Trim(cmdline, " ")
	idx := bytes.IndexFunc(cmdline, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	})
	if idx == -1 {
		return filepath.Base(string(cmdline))
	}
	return filepath.Base(string(cmdline[:idx]))
}

func isPid(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return !unicode.IsDigit(r)
	}) == -1
}

// resolveSocketByProcFS finds UID and inode from /proc/net/{tcp,tcp6,udp,udp6}.
// In TUN mode metadata sourceIP is often the gateway (e.g. fake-ip range), not
// the socket's real local address; we match by local port first and prefer
// exact IP+port when it matches.
func resolveSocketByProcFS(network string, ip netip.Addr, srcPort int) (uint32, uint32, error) {
	var proto string
	switch {
	case strings.HasPrefix(network, "tcp"):
		proto = "tcp"
	case strings.HasPrefix(network, "udp"):
		proto = "udp"
	default:
		return 0, 0, ErrInvalidNetwork
	}

	targetPort := uint16(srcPort)
	unmapped := ip.Unmap()
	files := []string{"/proc/net/" + proto, "/proc/net/" + proto + "6"}

	var bestUID, bestInode uint32
	found := false
	for _, filePath := range files {
		isV6 := strings.HasSuffix(filePath, "6")
		var matchIP netip.Addr
		if unmapped.Is4() {
			if isV6 {
				matchIP = netip.AddrFrom16(unmapped.As16())
			} else {
				matchIP = unmapped
			}
		} else {
			if !isV6 {
				continue
			}
			matchIP = unmapped
		}

		uid, inode, exact, err := searchProcNetFileByPort(filePath, matchIP, targetPort)
		if err != nil {
			continue
		}
		if exact {
			return uid, inode, nil
		}
		if !found || (bestUID == 0 && uid != 0) {
			bestUID = uid
			bestInode = inode
			found = true
		}
	}

	if found {
		return bestUID, bestInode, nil
	}
	return 0, 0, ErrNotFound
}

// searchProcNetFileByPort scans /proc/net/* for local_address matching targetPort.
// Exact IP+port wins; else port-only (skips inode==0 entries used by TIME_WAIT).
func searchProcNetFileByPort(filePath string, targetIP netip.Addr, targetPort uint16) (uid, inode uint32, exact bool, err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, 0, false, err
	}
	defer file.Close()

	isV6 := strings.HasSuffix(filePath, "6")
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0, false, ErrNotFound
	}

	var bestUID, bestInode uint32
	found := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		lineIP, linePort, parseErr := parseHexAddrPort(fields[1], isV6)
		if parseErr != nil || linePort != targetPort {
			continue
		}
		lineUID, parseErr := strconv.ParseUint(fields[7], 10, 32)
		if parseErr != nil {
			continue
		}
		lineInode, parseErr := strconv.ParseUint(fields[9], 10, 32)
		if parseErr != nil {
			continue
		}
		if lineIP == targetIP {
			return uint32(lineUID), uint32(lineInode), true, nil
		}
		if lineInode == 0 {
			continue
		}
		if !found || (bestUID == 0 && lineUID != 0) {
			bestUID = uint32(lineUID)
			bestInode = uint32(lineInode)
			found = true
		}
	}

	if found {
		return bestUID, bestInode, false, nil
	}
	return 0, 0, false, ErrNotFound
}

func parseHexAddrPort(value string, isV6 bool) (netip.Addr, uint16, error) {
	colon := strings.IndexByte(value, ':')
	if colon < 0 {
		return netip.Addr{}, 0, fmt.Errorf("invalid addr:port: %s", value)
	}
	port, err := strconv.ParseUint(value[colon+1:], 16, 16)
	if err != nil {
		return netip.Addr{}, 0, err
	}

	var addr netip.Addr
	if isV6 {
		addr, err = parseHexIPv6(value[:colon])
	} else {
		addr, err = parseHexIPv4(value[:colon])
	}
	return addr, uint16(port), err
}

func parseHexIPv4(value string) (netip.Addr, error) {
	if len(value) != 8 {
		return netip.Addr{}, fmt.Errorf("invalid ipv4 hex len: %d", len(value))
	}
	bytes, err := hex.DecodeString(value)
	if err != nil {
		return netip.Addr{}, err
	}
	var ip [4]byte
	if littleEndian {
		ip[0], ip[1], ip[2], ip[3] = bytes[3], bytes[2], bytes[1], bytes[0]
	} else {
		copy(ip[:], bytes)
	}
	return netip.AddrFrom4(ip), nil
}

func parseHexIPv6(value string) (netip.Addr, error) {
	if len(value) != 32 {
		return netip.Addr{}, fmt.Errorf("invalid ipv6 hex len: %d", len(value))
	}
	var ip [16]byte
	for i := 0; i < 4; i++ {
		bytes, err := hex.DecodeString(value[i*8 : (i+1)*8])
		if err != nil {
			return netip.Addr{}, err
		}
		if littleEndian {
			ip[i*4+0] = bytes[3]
			ip[i*4+1] = bytes[2]
			ip[i*4+2] = bytes[1]
			ip[i*4+3] = bytes[0]
		} else {
			copy(ip[i*4:(i+1)*4], bytes)
		}
	}
	return netip.AddrFrom16(ip), nil
}

var littleEndian = func() bool {
	value := uint32(0x01020304)
	return *(*byte)(unsafe.Pointer(&value)) == 0x04
}()
