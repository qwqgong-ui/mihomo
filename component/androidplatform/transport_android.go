//go:build android

package androidplatform

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"syscall"
	"time"
)

const platformRequestTimeout = 20 * time.Second

func openTun(spec tunSpec) (int, error) {
	_, descriptor, err := exchangePlatformRequest(platformRequest{
		Operation: "open_tun",
		Tun:       &spec,
	}, true)
	return descriptor, err
}

func exchangePlatformRequest(
	request platformRequest,
	expectDescriptor bool,
) (platformResponse, int, error) {
	var response platformResponse
	name := currentSocketName()
	if name == "" {
		return response, -1, errors.New("Android platform bridge is not configured")
	}

	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		return response, -1, err
	}
	defer connection.Close()
	if err = connection.SetDeadline(time.Now().Add(platformRequestTimeout)); err != nil {
		return response, -1, err
	}
	if err = json.NewEncoder(connection).Encode(request); err != nil {
		return response, -1, err
	}

	descriptor := -1
	marker := []byte{0}
	if expectDescriptor {
		control := make([]byte, syscall.CmsgSpace(4))
		_, controlLength, flags, _, readErr := connection.ReadMsgUnix(marker, control)
		if readErr != nil {
			return response, -1, readErr
		}
		if flags&syscall.MSG_CTRUNC != 0 {
			return response, -1, errors.New("Android platform descriptor message was truncated")
		}
		descriptor, err = parseDescriptor(control[:controlLength])
		if err != nil && marker[0] == 1 {
			return response, -1, err
		}
	} else if _, err = io.ReadFull(connection, marker); err != nil {
		return response, -1, err
	}

	line, err := bufio.NewReader(io.LimitReader(connection, 64*1024)).ReadBytes('\n')
	if err != nil {
		closeDescriptor(descriptor)
		return response, -1, err
	}
	if err = json.Unmarshal(line, &response); err != nil {
		closeDescriptor(descriptor)
		return response, -1, err
	}
	if marker[0] != 1 || !response.OK {
		closeDescriptor(descriptor)
		return response, -1, errors.New(nonEmpty(response.Error, "Android platform request failed"))
	}
	if expectDescriptor && descriptor < 0 {
		return response, -1, errors.New("Android platform response omitted the TUN descriptor")
	}
	return response, descriptor, nil
}

func parseDescriptor(control []byte) (int, error) {
	if len(control) == 0 {
		return -1, nil
	}
	messages, err := syscall.ParseSocketControlMessage(control)
	if err != nil {
		return -1, err
	}
	descriptor := -1
	for _, message := range messages {
		descriptors, parseErr := syscall.ParseUnixRights(&message)
		if parseErr != nil {
			return -1, parseErr
		}
		for _, candidate := range descriptors {
			if descriptor < 0 {
				descriptor = candidate
			} else {
				_ = syscall.Close(candidate)
			}
		}
	}
	return descriptor, nil
}

func closeDescriptor(descriptor int) {
	if descriptor >= 0 {
		_ = syscall.Close(descriptor)
	}
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
