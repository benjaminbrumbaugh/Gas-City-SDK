//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package routingdecision

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openBoltFile(name string, flag int, mode os.FileMode) (*os.File, error) {
	fd, err := unix.Open(name, flag|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode.Perm()))
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		unix.Close(fd) //nolint:errcheck
		return nil, errors.New("database file is not private regular storage")
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		unix.Close(fd) //nolint:errcheck
		return nil, errors.New("wrap database file")
	}
	return file, nil
}

func openExclusivePrivateFile(name string) (*os.File, error) {
	fd, err := unix.Open(name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		unix.Close(fd) //nolint:errcheck
		return nil, errors.New("wrap private file")
	}
	return file, nil
}
