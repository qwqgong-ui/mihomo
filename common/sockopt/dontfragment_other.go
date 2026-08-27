//go:build !linux && !android

package sockopt

func dontFragmentControl(fd uintptr) error { return nil }
