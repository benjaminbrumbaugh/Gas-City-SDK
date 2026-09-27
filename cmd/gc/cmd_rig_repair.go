package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads/contract"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/spf13/cobra"
)

// verifyRigRepairDatabase is a seam for the command's destructive-path guard.
// The live implementation only reads the named database and its authoritative
// project identity; the command never creates, migrates, or renames a database.
var (
	verifyRigRepairDatabase = verifyRigRepairDatabaseLive
	rigRepairDoltPort       = currentResolvableManagedDoltPort
)

func newRigRepairCmd(stdout, stderr io.Writer) *cobra.Command {
	var database string
	cmd := &cobra.Command{
		Use:   "repair <name>",
		Short: "Repair a rig's local metadata pointer and formula staging",
		Long: `Repair regenerable local rig materialization after verifying the
named Dolt database belongs to the rig's tracked beads identity file.

The repair writes only .beads/metadata.json and formula symlinks. It never
runs a beads migration, creates a database, or mutates Dolt data. Pass the
database name exactly as shown by the healthy Dolt server; a missing or
unreadable metadata pointer must not be guessed from a prefix.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdRigRepair(args[0], database, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
		ValidArgsFunction: completeRigNames,
	}
	cmd.Flags().StringVar(&database, "database", "", "verified Dolt database name for this rig (required)")
	_ = cmd.MarkFlagRequired("database")
	return cmd
}

func cmdRigRepair(rigName, database string, stdout, stderr io.Writer) int {
	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc rig repair: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	cfg, err := loadCityConfigWithoutBuiltinPackRefresh(cityPath, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "gc rig repair: loading config: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	resolveRigPaths(cityPath, cfg.Rigs)
	rig, ok := rigByName(cfg, rigName)
	if !ok {
		fmt.Fprintln(stderr, rigNotFoundMsg("gc rig repair", rigName, cfg)) //nolint:errcheck // best-effort stderr
		return 1
	}
	if strings.TrimSpace(rig.Path) == "" {
		fmt.Fprintf(stderr, "gc rig repair: rig %q has no path binding\n", rig.Name) //nolint:errcheck // best-effort stderr
		return 1
	}
	if !cityUsesBdStoreContract(cityPath) {
		fmt.Fprintln(stderr, "gc rig repair: only bd-backed Dolt rigs can use this repair path") //nolint:errcheck // best-effort stderr
		return 1
	}

	result, err := repairRigMaterialization(cityPath, cfg, rig, database)
	if err != nil {
		fmt.Fprintf(stderr, "gc rig repair: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	fmt.Fprintf(stdout, "repaired rig %s\nmetadata\t%s\tchanged=%t\ndatabase\t%s\tproject_id=%s\nformulas\t%s\tlinks=%d\n",
		rig.Name, result.MetadataPath, result.MetadataChanged, result.Database, result.ProjectID,
		result.FormulasPath, result.FormulaLinks)
	return 0
}

type rigRepairResult struct {
	MetadataPath    string
	MetadataChanged bool
	Database        string
	ProjectID       string
	FormulasPath    string
	FormulaLinks    int
}

func repairRigMaterialization(cityPath string, cfg *config.City, rig config.Rig, database string) (rigRepairResult, error) {
	if cfg == nil {
		return rigRepairResult{}, fmt.Errorf("city config is nil")
	}
	scopeRoot := filepath.Clean(rig.Path)
	identity, ok, err := contract.ReadProjectIdentity(fsys.OSFS{}, scopeRoot)
	if err != nil {
		return rigRepairResult{}, fmt.Errorf("read tracked project identity: %w", err)
	}
	if !ok {
		return rigRepairResult{}, fmt.Errorf("missing tracked %s; refusing to guess the database identity", contract.ProjectIdentityPath(scopeRoot))
	}
	database = strings.TrimSpace(database)
	if database == "" {
		return rigRepairResult{}, fmt.Errorf("missing --database; pass the verified Dolt database name")
	}
	port := rigRepairDoltPort(cityPath)
	if port == "" {
		return rigRepairResult{}, fmt.Errorf("managed Dolt port is unavailable; verify the healthy server and retry (no files changed)")
	}
	if err := verifyRigRepairDatabase(port, database, identity); err != nil {
		return rigRepairResult{}, err
	}

	formulaLayers := cfg.FormulaLayers.SearchPaths(rig.Name)
	if len(formulaLayers) > 0 {
		if err := ResolveFormulas(scopeRoot, formulaLayers); err != nil {
			return rigRepairResult{}, fmt.Errorf("restore formula staging: %w", err)
		}
	}
	metadataPath := filepath.Join(scopeRoot, ".beads", "metadata.json")
	changed, err := contract.EnsureCanonicalMetadata(fsys.OSFS{}, metadataPath, contract.MetadataState{
		Database:     "dolt",
		Backend:      "dolt",
		DoltMode:     "server",
		DoltDatabase: database,
	})
	if err != nil {
		return rigRepairResult{}, fmt.Errorf("regenerate metadata pointer: %w", err)
	}
	return rigRepairResult{
		MetadataPath:    metadataPath,
		MetadataChanged: changed,
		Database:        database,
		ProjectID:       identity,
		FormulasPath:    filepath.Join(scopeRoot, ".beads", "formulas"),
		FormulaLinks:    countFormulaLinks(filepath.Join(scopeRoot, ".beads", "formulas")),
	}, nil
}

func verifyRigRepairDatabaseLive(port, database, expectedProjectID string) error {
	db, err := managedDoltOpenDatabase("", port, "root", database)
	if err != nil {
		return fmt.Errorf("open verified Dolt database %q: %w", database, err)
	}
	defer db.Close() //nolint:errcheck // read-only verification
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping verified Dolt database %q: %w", database, err)
	}
	got, ok, err := readDatabaseProjectID(ctx, db)
	if err != nil {
		return fmt.Errorf("read verified database %q identity: %w", database, err)
	}
	if !ok {
		return fmt.Errorf("verified database %q has no metadata._project_id; refusing to guess the rig identity", database)
	}
	if got != expectedProjectID {
		return fmt.Errorf("verified database %q belongs to project_id %q, not tracked project_id %q", database, got, expectedProjectID)
	}
	return nil
}

func countFormulaLinks(path string) int {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".toml") && entry.Type()&os.ModeSymlink != 0 {
			count++
		}
	}
	return count
}
