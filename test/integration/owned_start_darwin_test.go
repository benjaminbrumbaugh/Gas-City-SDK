//go:build integration && darwin

package integration

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// ps lstart has second precision, which is insufficient for kill authority.
func integrationProcessStartTime(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if int(info.Proc.P_pid) != pid || info.Proc.P_starttime.Sec == 0 {
		return "", fmt.Errorf("missing kernel identity for PID %d", pid)
	}
	return fmt.Sprintf("%d:%d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec), nil
}
