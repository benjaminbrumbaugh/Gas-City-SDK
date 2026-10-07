//go:build !darwin && !windows

package sessionlog

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func codexQuotaDescriptorPath(file *os.File) (string, error) {
	fdPath := filepath.Join("/proc/self/fd", strconv.FormatUint(uint64(file.Fd()), 10))
	resolved, err := filepath.EvalSymlinks(fdPath)
	if err != nil || strings.HasSuffix(resolved, " (deleted)") {
		return "", fmt.Errorf("resolving opened session log descriptor")
	}
	absolute, err := filepath.Abs(filepath.Clean(resolved))
	if err != nil {
		return "", fmt.Errorf("resolving opened session log descriptor: %w", err)
	}
	return absolute, nil
}
