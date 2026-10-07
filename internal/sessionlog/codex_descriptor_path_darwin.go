//go:build darwin

package sessionlog

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func codexQuotaDescriptorPath(file *os.File) (string, error) {
	var path [4096]byte
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_GETPATH, uintptr(unsafe.Pointer(&path[0])))
	if errno != 0 {
		return "", fmt.Errorf("resolving opened session log descriptor: %w", errno)
	}
	resolved := path[:]
	if end := bytes.IndexByte(path[:], 0); end >= 0 {
		resolved = resolved[:end]
	}
	return string(resolved), nil
}
