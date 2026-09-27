package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads/contract"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
)

func TestRepairRigMaterializationVerifiesIdentityAndRestoresFormulas(t *testing.T) {
	cityPath := t.TempDir()
	rigPath := filepath.Join(cityPath, "dashboard")
	if err := os.MkdirAll(filepath.Join(rigPath, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := contract.WriteProjectIdentity(fsys.OSFS{}, rigPath, "project-dashboard"); err != nil {
		t.Fatal(err)
	}
	layer := filepath.Join(cityPath, "override-formulas")
	if err := os.MkdirAll(layer, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layer, "mol-polecat-work.toml"), []byte("name = 'mol-polecat-work'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	originalPort := rigRepairDoltPort
	originalVerify := verifyRigRepairDatabase
	t.Cleanup(func() {
		rigRepairDoltPort = originalPort
		verifyRigRepairDatabase = originalVerify
	})
	rigRepairDoltPort = func(string) string { return "3311" }
	verified := false
	verifyRigRepairDatabase = func(port, database, projectID string) error {
		if port != "3311" || database != "gcd" || projectID != "project-dashboard" {
			t.Fatalf("verification inputs = (%q, %q, %q)", port, database, projectID)
		}
		verified = true
		return nil
	}

	cfg := &config.City{
		Rigs: []config.Rig{{Name: "Gas-City-Dashboard", Path: rigPath}},
		FormulaLayers: config.FormulaLayers{
			City: []string{layer},
			Rigs: map[string][]string{"Gas-City-Dashboard": {layer}},
		},
	}
	result, err := repairRigMaterialization(cityPath, cfg, cfg.Rigs[0], "gcd")
	if err != nil {
		t.Fatalf("repairRigMaterialization: %v", err)
	}
	if !verified {
		t.Fatal("database identity was not verified before repair")
	}
	if !result.MetadataChanged {
		t.Fatal("metadata_changed = false, want newly regenerated pointer")
	}
	if result.FormulaLinks != 2 {
		t.Fatalf("formula links = %d, want canonical and legacy links", result.FormulaLinks)
	}
	for _, name := range []string{"mol-polecat-work.toml", "mol-polecat-work.formula.toml"} {
		path := filepath.Join(rigPath, ".beads", "formulas", name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("formula link %s: %v", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("formula path %s is %s, want symlink", name, info.Mode())
		}
	}
	database, ok, err := contract.ReadDoltDatabase(fsys.OSFS{}, filepath.Join(rigPath, ".beads", "metadata.json"))
	if err != nil || !ok || database != "gcd" {
		t.Fatalf("repaired database = (%q, %v, %v), want gcd", database, ok, err)
	}
}

func TestRepairRigMaterializationRefusesUnverifiedDatabaseWithoutWriting(t *testing.T) {
	cityPath := t.TempDir()
	rigPath := filepath.Join(cityPath, "dashboard")
	if err := os.MkdirAll(filepath.Join(rigPath, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := contract.WriteProjectIdentity(fsys.OSFS{}, rigPath, "project-dashboard"); err != nil {
		t.Fatal(err)
	}
	originalPort := rigRepairDoltPort
	originalVerify := verifyRigRepairDatabase
	t.Cleanup(func() {
		rigRepairDoltPort = originalPort
		verifyRigRepairDatabase = originalVerify
	})
	rigRepairDoltPort = func(string) string { return "3311" }
	verifyRigRepairDatabase = func(_, _, _ string) error {
		return errors.New("unverified database identity")
	}

	cfg := &config.City{Rigs: []config.Rig{{Name: "dashboard", Path: rigPath}}}
	_, err := repairRigMaterialization(cityPath, cfg, cfg.Rigs[0], "gcd")
	if err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("repairRigMaterialization error = %v, want verification refusal", err)
	}
	if _, err := os.Stat(filepath.Join(rigPath, ".beads", "metadata.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata exists after refused repair, stat err=%v", err)
	}
}
