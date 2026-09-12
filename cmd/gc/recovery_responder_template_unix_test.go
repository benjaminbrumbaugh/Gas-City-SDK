//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReadRecoveryWayfinderTemplateRejectsFIFOWIthoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := readRecoveryWayfinderTemplate(path); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("FIFO error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FIFO open blocked for %s", elapsed)
	}
}

func TestReadRecoveryWayfinderTemplateRejectsSwappedAncestorSymlink(t *testing.T) {
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

	templatePath := filepath.Join(templateDir, "request.json")
	if err := os.WriteFile(templatePath, []byte("trusted"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(outsideDir, "request.json")
	if err := os.WriteFile(outsidePath, []byte("attacker-controlled"), 0o600); err != nil {
		t.Fatal(err)
	}

	validatedPath, err := filepath.EvalSymlinks(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(templateDir, templateDir+".trusted"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, templateDir); err != nil {
		t.Fatal(err)
	}

	relativePath, err := filepath.Rel(cityRoot, validatedPath)
	if err != nil {
		t.Fatal(err)
	}
	body, err := readRecoveryWayfinderTemplateAt(cityRoot, relativePath)
	if err == nil {
		t.Fatalf("read through swapped ancestor symlink: %q", body)
	}
}
