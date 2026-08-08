//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package routingdecision

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

func readCityStateFile(cityRoot, name string, maxBytes int64, ownerOnly bool) ([]byte, error) {
	for _, path := range []string{cityRoot, filepath.Join(cityRoot, ".gc")} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe state path")
		}
	}
	path := filepath.Join(cityRoot, ".gc", name)
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("unsafe state file")
	}
	if ownerOnly && before.Mode().Perm() != 0o400 && before.Mode().Perm() != 0o600 {
		return nil, errors.New("secure input is not owner-only")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() > maxBytes {
		return nil, errors.New("state file identity changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, errors.New("read bounded state file")
	}
	return data, nil
}
