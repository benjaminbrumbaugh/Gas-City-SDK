//go:build integration && !darwin

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func integrationProcessStartTime(pid int) (string, error) {
	if runtime.GOOS != "linux" || pid <= 0 {
		return "", fmt.Errorf("no verified kill identity for PID %d on %s", pid, runtime.GOOS)
	}
	// Unlike a liveness probe, kill authority must never fall back to ps's
	// second-resolution date when the exact kernel start token is unavailable.
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	stat := string(data)
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", fmt.Errorf("malformed kernel identity for PID %d", pid)
	}
	fields := strings.Fields(stat[end+1:])
	const startIndex = 19 // field 22, relative to field 3 after comm
	if len(fields) <= startIndex {
		return "", fmt.Errorf("missing kernel start token for PID %d", pid)
	}
	if _, err := strconv.ParseUint(fields[startIndex], 10, 64); err != nil {
		return "", fmt.Errorf("invalid kernel start token for PID %d: %w", pid, err)
	}
	return fields[startIndex], nil
}
