//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const maximumRecoveryWayfinderTemplateBytes = 1 << 20

func readRecoveryWayfinderTemplate(path string) ([]byte, error) {
	return readRecoveryWayfinderTemplateAt(filepath.Dir(path), filepath.Base(path))
}

// readRecoveryWayfinderTemplateAt pins the city root before traversing the
// relative template path. A rename of an opened directory cannot redirect a
// later open, and O_NOFOLLOW rejects replacements that are symlinks.
func readRecoveryWayfinderTemplateAt(root, relativePath string) ([]byte, error) {
	rootFD, err := openRecoveryWayfinderPath(root, true, 0)
	if err != nil {
		return nil, fmt.Errorf("open city root: %w", err)
	}
	fd, err := openRecoveryWayfinderRelative(rootFD, relativePath)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), relativePath)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open returned an invalid file")
	}
	defer file.Close() //nolint:errcheck // read-only descriptor
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("template is not a regular file")
	}
	body, err := io.ReadAll(io.LimitReader(file, maximumRecoveryWayfinderTemplateBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maximumRecoveryWayfinderTemplateBytes {
		return nil, fmt.Errorf("template exceeds %d bytes", maximumRecoveryWayfinderTemplateBytes)
	}
	return body, nil
}

const maximumRootSymlinkExpansions = 8

func openRecoveryWayfinderPath(path string, finalDirectory bool, rootSymlinkExpansions int) (int, error) {
	cleanPath := filepath.Clean(path)
	absolute := filepath.IsAbs(cleanPath)
	startPath := "."
	if absolute {
		startPath = string(filepath.Separator)
		cleanPath = strings.TrimPrefix(cleanPath, startPath)
	}
	components := strings.Split(cleanPath, string(filepath.Separator))
	for _, component := range components {
		if component == ".." {
			return -1, fmt.Errorf("template path must not contain parent traversal")
		}
	}

	currentFD, err := unix.Open(startPath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	for index, component := range components {
		if component == "" || component == "." {
			continue
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOFOLLOW
		if index < len(components)-1 || finalDirectory {
			flags |= unix.O_DIRECTORY
		}
		nextFD, openErr := unix.Openat(currentFD, component, flags, 0)
		if openErr == nil {
			_ = unix.Close(currentFD)
			currentFD = nextFD
			continue
		}

		// Some Unix roots contain administrator-owned compatibility symlinks
		// (for example, /var on macOS). Resolve only that first root entry via
		// the already-open root descriptor. Every user-mutable descendant still
		// has to open with O_NOFOLLOW.
		if absolute && index == 0 && rootSymlinkExpansions < maximumRootSymlinkExpansions {
			target, readlinkErr := readRootSymlink(currentFD, component)
			if readlinkErr == nil {
				_ = unix.Close(currentFD)
				if !filepath.IsAbs(target) {
					target = filepath.Join(startPath, target)
				}
				remaining := append([]string{target}, components[index+1:]...)
				return openRecoveryWayfinderPath(filepath.Join(remaining...), finalDirectory, rootSymlinkExpansions+1)
			}
		}
		_ = unix.Close(currentFD)
		return -1, fmt.Errorf("open template path component %q: %w", component, openErr)
	}
	return currentFD, nil
}

func openRecoveryWayfinderRelative(rootFD int, relativePath string) (int, error) {
	cleanPath := filepath.Clean(relativePath)
	if filepath.IsAbs(cleanPath) || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		_ = unix.Close(rootFD)
		return -1, fmt.Errorf("template path must stay beneath city root")
	}
	components := strings.Split(cleanPath, string(filepath.Separator))
	currentFD := rootFD
	for index, component := range components {
		if component == "" || component == "." {
			continue
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOFOLLOW
		if index < len(components)-1 {
			flags |= unix.O_DIRECTORY
		}
		nextFD, err := unix.Openat(currentFD, component, flags, 0)
		_ = unix.Close(currentFD)
		if err != nil {
			return -1, fmt.Errorf("open template path component %q: %w", component, err)
		}
		currentFD = nextFD
	}
	return currentFD, nil
}

func readRootSymlink(rootFD int, name string) (string, error) {
	for size := 256; size <= 64*1024; size *= 2 {
		buffer := make([]byte, size)
		count, err := unix.Readlinkat(rootFD, name, buffer)
		if err != nil {
			return "", err
		}
		if count < len(buffer) {
			return string(buffer[:count]), nil
		}
	}
	return "", fmt.Errorf("root symlink target is too long")
}
