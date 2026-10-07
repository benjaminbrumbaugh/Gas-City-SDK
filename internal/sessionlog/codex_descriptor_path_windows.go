//go:build windows

package sessionlog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func codexQuotaDescriptorPath(file *os.File) (string, error) {
	for size := uint32(256); size <= 32768; size *= 2 {
		path := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &path[0], size, 0)
		if err != nil {
			return "", fmt.Errorf("resolving opened session log descriptor: %w", err)
		}
		if n >= size-1 {
			continue
		}
		resolved := windows.UTF16ToString(path[:n])
		resolved = strings.TrimPrefix(resolved, `\\?\`)
		if strings.HasPrefix(resolved, `UNC\`) {
			resolved = `\\` + strings.TrimPrefix(resolved, `UNC\`)
		}
		return filepath.Clean(resolved), nil
	}
	return "", fmt.Errorf("resolving opened session log descriptor: path is too long")
}
