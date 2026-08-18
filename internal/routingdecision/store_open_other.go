//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package routingdecision

import "os"

func openBoltFile(name string, flag int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(name, flag, mode)
}

func openExclusivePrivateFile(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}
