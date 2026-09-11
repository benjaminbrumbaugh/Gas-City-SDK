//go:build windows

package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func acquireProviderFenceFileLock(ctx context.Context, path string, exclusive bool) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating provider fence lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening provider fence lock: %w", err)
	}
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	var overlapped windows.Overlapped
	if err := waitForProviderFenceFileLock(ctx, func() (bool, error) {
		err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &overlapped)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
			return false, nil
		default:
			return false, fmt.Errorf("acquiring provider fence lock: %w", err)
		}
	}); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
		_ = file.Close()
	}, nil
}
