//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type recoveryJunctionName struct {
	offset uint16
	length uint16
}

type recoveryJunctionTarget struct {
	substitute recoveryJunctionName
	print      recoveryJunctionName
	path       []uint16
}

func (t *recoveryJunctionTarget) add(name string) recoveryJunctionName {
	encoded := syscall.StringToUTF16(name)
	position := recoveryJunctionName{offset: uint16(len(t.path) * 2), length: uint16((len(encoded) - 1) * 2)}
	t.path = append(t.path, encoded...)
	return position
}

type recoveryReparseDataBuffer struct {
	header recoveryReparseDataBufferHeader
	detail [syscall.MAXIMUM_REPARSE_DATA_BUFFER_SIZE]byte
}

type recoveryReparseDataBufferHeader struct {
	reparseTag        uint32
	reparseDataLength uint16
	reserved          uint16
}

type recoveryMountPointReparseBuffer struct {
	substituteNameOffset uint16
	substituteNameLength uint16
	printNameOffset      uint16
	printNameLength      uint16
	pathBuffer           [1]uint16
}

func createRecoveryJunction(link, target string) error {
	var names recoveryJunctionTarget
	names.substitute = names.add(`\??\` + target)
	names.print = names.add(target)
	var mount *recoveryMountPointReparseBuffer
	dataLength := uint16(unsafe.Offsetof(mount.pathBuffer)) + uint16(len(names.path)*2)
	data := make([]byte, dataLength)
	mount = (*recoveryMountPointReparseBuffer)(unsafe.Pointer(&data[0]))
	mount.substituteNameOffset = names.substitute.offset
	mount.substituteNameLength = names.substitute.length
	mount.printNameOffset = names.print.offset
	mount.printNameLength = names.print.length
	copy((*[2048]uint16)(unsafe.Pointer(&mount.pathBuffer[0]))[:len(names.path):len(names.path)], names.path)

	var reparse recoveryReparseDataBuffer
	reparse.header.reparseTag = windows.IO_REPARSE_TAG_MOUNT_POINT
	reparse.header.reparseDataLength = dataLength
	copy(reparse.detail[:], data)
	if err := os.Mkdir(link, 0o700); err != nil {
		return err
	}
	path, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle) //nolint:errcheck // test helper cleanup
	length := uint32(reparse.header.reparseDataLength) + uint32(unsafe.Sizeof(reparse.header))
	var returned uint32
	return windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT,
		(*byte)(unsafe.Pointer(&reparse.header)), length, nil, 0, &returned, nil)
}

func TestReadRecoveryWayfinderTemplateWindowsReadsNestedRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "request.json"), []byte("trusted"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := readRecoveryWayfinderTemplateAt(root, filepath.Join("config", "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "trusted" {
		t.Fatalf("body = %q, want trusted", body)
	}
}

func TestReadRecoveryWayfinderTemplateWindowsRejectsSwappedAncestorJunction(t *testing.T) {
	root := t.TempDir()
	cityRoot := filepath.Join(root, "city")
	templateDir := filepath.Join(cityRoot, "config")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(templateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "request.json"), []byte("trusted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "request.json"), []byte("attacker-controlled"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(templateDir, templateDir+".trusted"); err != nil {
		t.Fatal(err)
	}
	if err := createRecoveryJunction(templateDir, outsideDir); err != nil {
		t.Fatal(err)
	}

	body, err := readRecoveryWayfinderTemplateAt(cityRoot, filepath.Join("config", "request.json"))
	if err == nil {
		t.Fatalf("read through swapped ancestor junction: %q", body)
	}
}

func TestReadRecoveryWayfinderTemplateWindowsRejectsFinalFileSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside.json")
	link := filepath.Join(root, "request.json")
	if err := os.WriteFile(outside, []byte("attacker-controlled"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		if errorsIsPrivilegeFailure(err) {
			t.Skipf("creating file symlink requires Windows developer mode or privilege: %v", err)
		}
		t.Fatal(err)
	}
	body, err := readRecoveryWayfinderTemplateAt(root, "request.json")
	if err == nil {
		t.Fatalf("read through final file symlink: %q", body)
	}
}

func errorsIsPrivilegeFailure(err error) bool {
	return errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD)
}
