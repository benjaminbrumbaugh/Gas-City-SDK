//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const maximumRecoveryWayfinderTemplateBytes = 1 << 20

func readRecoveryWayfinderTemplate(path string) ([]byte, error) {
	return readRecoveryWayfinderTemplateAt(filepath.Dir(path), filepath.Base(path))
}

// readRecoveryWayfinderTemplateAt pins the city root, then opens every child
// relative to its already-open parent. OBJ_DONT_REPARSE and the post-open
// attribute check reject symlinks, junctions, and every other reparse point.
func readRecoveryWayfinderTemplateAt(root, relativePath string) ([]byte, error) {
	rootHandle, err := openRecoveryWayfinderWindowsRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open city root: %w", err)
	}
	handle, err := openRecoveryWayfinderWindowsRelative(rootHandle, relativePath)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), relativePath)
	if file == nil {
		_ = windows.CloseHandle(handle)
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

func openRecoveryWayfinderWindowsRoot(root string) (windows.Handle, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return windows.InvalidHandle, err
	}
	absolute = filepath.Clean(absolute)
	volume := filepath.VolumeName(absolute)
	if !validRecoveryWayfinderWindowsVolume(volume) {
		return windows.InvalidHandle, fmt.Errorf("unsupported city root volume %q", volume)
	}
	anchorPath := volume + string(filepath.Separator)
	anchor, err := openRecoveryWayfinderWindowsAnchor(anchorPath)
	if err != nil {
		return windows.InvalidHandle, err
	}
	components, err := recoveryWayfinderWindowsComponents(strings.TrimLeft(absolute[len(volume):], `\/`), true)
	if err != nil {
		_ = windows.CloseHandle(anchor)
		return windows.InvalidHandle, err
	}
	return traverseRecoveryWayfinderWindows(anchor, components, true)
}

func validRecoveryWayfinderWindowsVolume(volume string) bool {
	if len(volume) == 2 && volume[1] == ':' && ((volume[0] >= 'A' && volume[0] <= 'Z') || (volume[0] >= 'a' && volume[0] <= 'z')) {
		return true
	}
	if !strings.HasPrefix(volume, `\\`) || strings.HasPrefix(volume, `\\?\`) || strings.HasPrefix(volume, `\\.\`) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(volume, `\\`), `\`)
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func openRecoveryWayfinderWindowsAnchor(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return windows.InvalidHandle, err
	}
	if err := validateRecoveryWayfinderWindowsHandle(handle, true); err != nil {
		_ = windows.CloseHandle(handle)
		return windows.InvalidHandle, err
	}
	return handle, nil
}

// openRecoveryWayfinderWindowsRelative takes ownership of rootHandle.
func openRecoveryWayfinderWindowsRelative(rootHandle windows.Handle, relativePath string) (windows.Handle, error) {
	components, err := recoveryWayfinderWindowsComponents(relativePath, false)
	if err != nil {
		_ = windows.CloseHandle(rootHandle)
		return windows.InvalidHandle, err
	}
	if len(components) == 0 {
		_ = windows.CloseHandle(rootHandle)
		return windows.InvalidHandle, fmt.Errorf("template path must name a file beneath city root")
	}
	return traverseRecoveryWayfinderWindows(rootHandle, components, false)
}

func recoveryWayfinderWindowsComponents(path string, allowEmpty bool) ([]string, error) {
	cleanPath := filepath.Clean(path)
	if cleanPath == "." && allowEmpty {
		return nil, nil
	}
	if !filepath.IsLocal(cleanPath) {
		return nil, fmt.Errorf("template path must stay beneath city root")
	}
	components := strings.FieldsFunc(cleanPath, func(r rune) bool { return r == '\\' || r == '/' })
	for _, component := range components {
		if component == "" || component == "." || component == ".." || strings.ContainsRune(component, ':') {
			return nil, fmt.Errorf("invalid template path component %q", component)
		}
	}
	return components, nil
}

// traverseRecoveryWayfinderWindows takes ownership of current and returns
// ownership of the final handle to its caller.
func traverseRecoveryWayfinderWindows(current windows.Handle, components []string, finalDirectory bool) (windows.Handle, error) {
	for index, component := range components {
		directory := index < len(components)-1 || finalDirectory
		next, err := openRecoveryWayfinderWindowsComponent(current, component, directory)
		_ = windows.CloseHandle(current)
		if err != nil {
			return windows.InvalidHandle, fmt.Errorf("open template path component %q: %w", component, err)
		}
		current = next
	}
	return current, nil
}

func openRecoveryWayfinderWindowsComponent(parent windows.Handle, name string, directory bool) (windows.Handle, error) {
	encoded, err := windows.UTF16FromString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	objectName := windows.NTUnicodeString{
		Length:        uint16((len(encoded) - 1) * 2),
		MaximumLength: uint16(len(encoded) * 2),
		Buffer:        &encoded[0],
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    &objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	options := uint32(windows.FILE_OPEN_FOR_BACKUP_INTENT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	var handle windows.Handle
	err = windows.NtCreateFile(
		&handle,
		windows.FILE_GENERIC_READ,
		&attributes,
		&windows.IO_STATUS_BLOCK{},
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		options,
		0,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, err
	}
	if err := validateRecoveryWayfinderWindowsHandle(handle, directory); err != nil {
		_ = windows.CloseHandle(handle)
		return windows.InvalidHandle, err
	}
	return handle, nil
}

func validateRecoveryWayfinderWindowsHandle(handle windows.Handle, directory bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("template path contains a reparse point")
	}
	isDirectory := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if directory && !isDirectory {
		return fmt.Errorf("template path component is not a directory")
	}
	if !directory && isDirectory {
		return fmt.Errorf("template is not a regular file")
	}
	return nil
}
