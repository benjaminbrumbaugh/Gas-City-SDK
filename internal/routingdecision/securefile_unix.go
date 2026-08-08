//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package routingdecision

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func readCityStateFile(cityRoot, name string, maxBytes int64, ownerOnly bool) ([]byte, error) {
	rootFD, err := unix.Open(cityRoot, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(rootFD) //nolint:errcheck
	stateFD, err := unix.Openat(rootFD, ".gc", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(stateFD) //nolint:errcheck
	fd, err := unix.Openat(stateFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		unix.Close(fd) //nolint:errcheck
		return nil, errors.New("wrap secure file")
	}
	defer file.Close() //nolint:errcheck
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Size > maxBytes {
		return nil, errors.New("secure input is not a bounded regular file")
	}
	if ownerOnly && (stat.Mode&0o777 != 0o400 && stat.Mode&0o777 != 0o600 || stat.Uid != uint32(os.Geteuid())) {
		return nil, errors.New("secure input is not owner-only")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("read bounded secure input")
	}
	return data, nil
}
