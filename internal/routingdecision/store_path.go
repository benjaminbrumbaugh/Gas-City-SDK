package routingdecision

import (
	"errors"
	"os"
	"path/filepath"
)

func prepareStorePath(cityRoot string) (string, bool, error) {
	rootInfo, err := os.Lstat(cityRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, ErrStoreCorrupt
	}
	statePath := filepath.Join(cityRoot, ".gc")
	stateInfo, err := os.Lstat(statePath)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(statePath, 0o700); err != nil {
			return "", false, ErrStoreCorrupt
		}
		stateInfo, err = os.Lstat(statePath)
	}
	if err != nil || !stateInfo.IsDir() || stateInfo.Mode()&os.ModeSymlink != 0 {
		return "", false, ErrStoreCorrupt
	}
	path := filepath.Join(cityRoot, StoreRelativePath)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 || info.Size() == 0 {
		return "", false, ErrStoreCorrupt
	}
	return path, true, nil
}

func removeExactNewStore(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return err
	}
	return os.Remove(path)
}
