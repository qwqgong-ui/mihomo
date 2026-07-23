//go:build android && cgo

package main

/*
#include <stdlib.h>

extern int androidcyaml_protect_socket(int fd);
extern char* androidcyaml_resolve_process(
    int protocol,
    const char* source_address,
    int source_port,
    const char* destination_address,
    int destination_port
);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/metacubex/mihomo/component/androidplatform"
	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/process"
	"github.com/metacubex/mihomo/config"
	MC "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/hub/route"
)

type nativeResponse struct {
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

var (
	runtimeMu sync.Mutex
	active    bool
)

func main() {}

//export AndroidCyamlFree
func AndroidCyamlFree(value *C.char) {
	if value != nil {
		C.free(unsafe.Pointer(value))
	}
}

//export AndroidCyamlValidate
func AndroidCyamlValidate(homeValue, configValue *C.char) *C.char {
	home := C.GoString(homeValue)
	configPath := C.GoString(configValue)
	if err := initializeHome(home); err != nil {
		return respond(nil, err)
	}
	configuration, err := os.ReadFile(configPath)
	if err == nil {
		_, err = executor.ParseWithBytes(configuration)
	}
	return respond(nil, err)
}

//export AndroidCyamlPrepareTun
func AndroidCyamlPrepareTun(
	homeValue,
	configValue,
	stackValue *C.char,
	ipv6Value,
	processMatchingValue C.int,
) *C.char {
	home := C.GoString(homeValue)
	configPath := C.GoString(configValue)
	if err := initializeHome(home); err != nil {
		return respond(nil, err)
	}
	configuration, err := os.ReadFile(configPath)
	if err != nil {
		return respond(nil, err)
	}
	cfg, err := executor.ParseWithBytes(configuration)
	if err != nil {
		return respond(nil, err)
	}
	payload, err := androidplatform.PrepareEmbeddedConfig(cfg, androidplatform.EmbeddedOptions{
		FileDescriptor:  -1,
		Stack:           C.GoString(stackValue),
		IPv6Enabled:     ipv6Value != 0,
		ProcessMatching: processMatchingValue != 0,
	})
	return respond(payload, err)
}

//export AndroidCyamlStart
func AndroidCyamlStart(
	homeValue,
	configValue,
	uiValue,
	controllerValue,
	secretValue,
	stackValue *C.char,
	fileDescriptor,
	ipv6Value,
	processMatchingValue C.int,
) *C.char {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()

	if active {
		stopLocked()
	}

	home := C.GoString(homeValue)
	configPath := C.GoString(configValue)
	if err := initializeRuntimePaths(home, configPath); err != nil {
		return respond(nil, err)
	}
	cfg, err := executor.ParseWithPath(configPath)
	if err != nil {
		return respond(nil, err)
	}

	cfg.Controller.ExternalUI = C.GoString(uiValue)
	cfg.Controller.ExternalController = C.GoString(controllerValue)
	cfg.Controller.Secret = C.GoString(secretValue)
	_, err = androidplatform.PrepareEmbeddedConfig(cfg, androidplatform.EmbeddedOptions{
		FileDescriptor:  int(fileDescriptor),
		Stack:           C.GoString(stackValue),
		IPv6Enabled:     ipv6Value != 0,
		ProcessMatching: processMatchingValue != 0,
	})
	if err != nil {
		return respond(nil, err)
	}

	installPlatformHooks()
	route.SetEmbedMode(true)
	hub.ApplyConfig(cfg)
	active = true
	return respond(nil, nil)
}

//export AndroidCyamlStop
func AndroidCyamlStop() *C.char {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	stopLocked()
	return respond(nil, nil)
}

//export AndroidCyamlIsRunning
func AndroidCyamlIsRunning() C.int {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if active {
		return 1
	}
	return 0
}

//export AndroidCyamlTrimMemory
func AndroidCyamlTrimMemory() C.int {
	runtime.GC()
	debug.FreeOSMemory()
	return 1
}

func initializeHome(home string) error {
	if home == "" || !filepath.IsAbs(home) {
		return errors.New("mihomo home directory must be absolute")
	}
	MC.SetHomeDir(home)
	return config.Init(home)
}

func initializeRuntimePaths(home, configPath string) error {
	if err := initializeHome(home); err != nil {
		return err
	}
	if configPath == "" || !filepath.IsAbs(configPath) {
		return errors.New("mihomo configuration path must be absolute")
	}
	MC.SetConfig(configPath)
	return nil
}

func installPlatformHooks() {
	dialer.DefaultSocketHook = func(network, address string, connection syscall.RawConn) error {
		var rejected bool
		err := connection.Control(func(fileDescriptor uintptr) {
			rejected = C.androidcyaml_protect_socket(C.int(fileDescriptor)) == 0
		})
		if err != nil {
			return err
		}
		if rejected {
			return fmt.Errorf("VpnService.protect rejected %s socket for %s", network, address)
		}
		return nil
	}
	process.DefaultProcessNameResolver = resolveProcess
}

func clearPlatformHooks() {
	dialer.DefaultSocketHook = nil
	process.DefaultProcessNameResolver = nil
}

func resolveProcess(network string, source, destination netip.AddrPort) (uint32, string, error) {
	if !source.IsValid() || !destination.IsValid() {
		return 0, "", process.ErrNotFound
	}
	var protocol int
	switch {
	case strings.HasPrefix(network, "tcp"):
		protocol = syscall.IPPROTO_TCP
	case strings.HasPrefix(network, "udp"):
		protocol = syscall.IPPROTO_UDP
	default:
		return 0, "", process.ErrInvalidNetwork
	}

	sourceAddress := C.CString(source.Addr().String())
	destinationAddress := C.CString(destination.Addr().String())
	defer C.free(unsafe.Pointer(sourceAddress))
	defer C.free(unsafe.Pointer(destinationAddress))

	encoded := C.androidcyaml_resolve_process(
		C.int(protocol),
		sourceAddress,
		C.int(source.Port()),
		destinationAddress,
		C.int(destination.Port()),
	)
	if encoded == nil {
		return 0, "", process.ErrNotFound
	}
	defer C.free(unsafe.Pointer(encoded))
	uidValue, packageName, found := strings.Cut(C.GoString(encoded), "\n")
	if !found || packageName == "" {
		return 0, "", process.ErrNotFound
	}
	uid, err := strconv.ParseUint(uidValue, 10, 32)
	if err != nil {
		return 0, "", process.ErrNotFound
	}
	return uint32(uid), packageName, nil
}

func stopLocked() {
	if active {
		executor.Shutdown()
		route.ReCreateServer(&route.Config{})
	}
	clearPlatformHooks()
	active = false
	runtime.GC()
}

func respond(payload []byte, err error) *C.char {
	response := nativeResponse{OK: err == nil}
	if err != nil {
		response.Error = err.Error()
	} else if len(payload) != 0 {
		response.Payload = json.RawMessage(payload)
	}
	encoded, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		encoded = []byte(`{"ok":false,"error":"unable to encode native response"}`)
	}
	return C.CString(string(encoded))
}
