package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/routingdecision"
	"github.com/spf13/cobra"
)

func newRouteDecisionCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "route-decision", Short: "Offline durable routing-decision maintenance", Hidden: true}
	cmd.AddCommand(
		newRouteDecisionImportLegacyCmd(stdout, stderr),
		newRouteDecisionBackupCmd(stdout, stderr),
		newRouteDecisionExportCmd(stdout, stderr),
		newRouteDecisionVerifyCmd(stdout, stderr),
	)
	return cmd
}

func newRouteDecisionImportLegacyCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "import-legacy --city <city>", Hidden: true, Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		result, err := runStoppedRouteDecisionOperation(cmd, true, func(cityPath string) (any, error) {
			return importLegacyRoutingDecisions(cmd.Context(), cityPath)
		})
		if err != nil {
			fmt.Fprintf(stderr, "gc route-decision import-legacy: %v\n", err) //nolint:errcheck
			return errExit
		}
		return writeCLIJSONLine(stdout, result)
	}
	return cmd
}

func newRouteDecisionBackupCmd(stdout, stderr io.Writer) *cobra.Command {
	var output string
	cmd := &cobra.Command{Use: "backup --city <city> --output <path>", Hidden: true, Args: cobra.NoArgs}
	cmd.Flags().StringVar(&output, "output", "", "new backup file path")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("output") || strings.TrimSpace(output) == "" {
			fmt.Fprintln(stderr, "gc route-decision backup: --output is required") //nolint:errcheck
			return errExit
		}
		result, err := runStoppedRouteDecisionOperation(cmd, false, func(cityPath string) (any, error) {
			store, err := routingdecision.OpenStore(cityPath, routingdecision.StoreOptions{})
			if err != nil {
				return nil, errors.New("ledger unavailable")
			}
			defer store.Close() //nolint:errcheck
			if err := store.Backup(output); err != nil {
				return nil, errors.New("backup failed")
			}
			return map[string]string{"backup": output}, nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gc route-decision backup: %v\n", err) //nolint:errcheck
			return errExit
		}
		return writeCLIJSONLine(stdout, result)
	}
	return cmd
}

func newRouteDecisionExportCmd(stdout, stderr io.Writer) *cobra.Command {
	var output string
	cmd := &cobra.Command{Use: "export --city <city> [--output <path>]", Hidden: true, Args: cobra.NoArgs}
	cmd.Flags().StringVar(&output, "output", "", "new export file path (default stdout)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		_, err := runStoppedRouteDecisionOperation(cmd, false, func(cityPath string) (any, error) {
			store, err := routingdecision.OpenStore(cityPath, routingdecision.StoreOptions{})
			if err != nil {
				return nil, errors.New("ledger unavailable")
			}
			defer store.Close() //nolint:errcheck
			writer := stdout
			var file *os.File
			if strings.TrimSpace(output) != "" {
				file, err = os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					return nil, errors.New("export destination unavailable")
				}
				defer file.Close() //nolint:errcheck
				writer = file
			}
			if err := store.Export(writer); err != nil {
				return nil, errors.New("export failed")
			}
			return struct{}{}, nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gc route-decision export: %v\n", err) //nolint:errcheck
			return errExit
		}
		return nil
	}
	return cmd
}

func newRouteDecisionVerifyCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "verify --city <city>", Hidden: true, Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		result, err := runStoppedRouteDecisionOperation(cmd, false, func(cityPath string) (any, error) {
			verifier, err := routingdecision.LoadAuthorityFile(cityPath)
			if err != nil {
				return nil, errors.New("authority input unavailable")
			}
			store, err := routingdecision.OpenStore(cityPath, routingdecision.StoreOptions{})
			if err != nil {
				return nil, errors.New("ledger unavailable")
			}
			defer store.Close() //nolint:errcheck
			report, err := store.Verify(verifier)
			if err != nil {
				return nil, errors.New("verification failed")
			}
			return report, nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "gc route-decision verify: %v\n", err) //nolint:errcheck
			return errExit
		}
		return writeCLIJSONLine(stdout, result)
	}
	return cmd
}

func runStoppedRouteDecisionOperation(cmd *cobra.Command, allowMissingLedger bool, operation func(string) (any, error)) (any, error) {
	if !cmd.Flags().Changed("city") || strings.TrimSpace(cityFlag) == "" {
		return nil, errors.New("--city is required")
	}
	if strings.TrimSpace(rigFlag) != "" || cmd.Flags().Changed("rig") {
		return nil, errors.New("--rig is not supported")
	}
	if strings.TrimSpace(contextFlag) != "" || strings.TrimSpace(cityURLFlag) != "" || strings.TrimSpace(cityNameFlag) != "" || readRemoteSelection().hasExplicitRemote() {
		return nil, errors.New("remote city selection is not supported")
	}
	cityPath, err := resolveCityFlagValue(cityFlag)
	if err != nil {
		return nil, errors.New("city unavailable")
	}
	lock, err := requireStoppedLocalCity(cityPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()
	if !allowMissingLedger {
		info, err := os.Lstat(filepath.Join(cityPath, routingdecision.StoreRelativePath))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("ledger unavailable")
		}
	}
	return operation(cityPath)
}

func importLegacyRoutingDecisions(ctx context.Context, cityPath string) (routingdecision.LegacyImportResult, error) {
	source, err := routingdecision.LoadLegacySource(cityPath)
	if err != nil {
		return routingdecision.LegacyImportResult{}, errors.New("legacy source unavailable")
	}
	cfg, err := loadCityConfigWithoutBuiltinPackRefresh(cityPath, io.Discard)
	if err != nil {
		return routingdecision.LegacyImportResult{}, errors.New("city config unavailable")
	}
	stores := make(map[string]beads.Store)
	payloads := make([]routingdecision.DecisionPayload, 0, len(source.Envelope.Intents))
	now := time.Now().UTC()
	resolver := &CityRuntime{cityPath: cityPath, cityName: cfg.Workspace.Name, cfg: cfg}
	for _, legacy := range source.Envelope.Intents {
		if legacy.City != cfg.Workspace.Name || !legacy.CreatedAt.Before(legacy.ExpiresAt) || !legacy.IsActiveAt(now) {
			return routingdecision.LegacyImportResult{}, errors.New("legacy decision is not currently eligible")
		}
		store := stores[legacy.Rig]
		if store == nil {
			store, err = openExistingRoutingDecisionWorkStore(ctx, cityPath, cfg, legacy.Rig)
			if err != nil {
				return routingdecision.LegacyImportResult{}, errors.New("work store unavailable")
			}
			stores[legacy.Rig] = store
		}
		work, err := beads.HandlesFor(store).Live.Get(legacy.WorkBeadID)
		if err != nil || work.Status != "open" || strings.TrimSpace(work.Assignee) != "" || work.DeferUntil != nil && now.Before(*work.DeferUntil) || work.IsBlocked != nil && *work.IsBlocked || routingDecisionHasExistingControlMetadata(work.Metadata) {
			return routingdecision.LegacyImportResult{}, errors.New("legacy work is not fresh")
		}
		ready, err := beads.HandlesFor(store).Live.Ready(beads.ReadyQuery{TierMode: beads.TierBoth})
		if err != nil || !containsExactReadyWork(ready, work.ID) {
			return routingdecision.LegacyImportResult{}, errors.New("legacy work is not ready")
		}
		state := routingdecision.WorkStateFrom(work.ID, work.Status, work.Assignee, work.Metadata, work.ClaimFence)
		stateDigest := routingdecision.WorkStateDigest(state)
		if strings.TrimPrefix(legacy.ExpectedStateDigest, "sha256:") != stateDigest {
			return routingdecision.LegacyImportResult{}, errors.New("legacy work binding changed")
		}
		_, targetDigest, ok := resolver.resolveRoutingDecisionTarget(legacy.Target, legacy.Rig)
		if !ok {
			return routingdecision.LegacyImportResult{}, errors.New("legacy target unavailable")
		}
		alternatives := make([]routingdecision.Alternative, 0, len(legacy.Fallbacks))
		for _, fallback := range legacy.Fallbacks {
			alternatives = append(alternatives, routingdecision.Alternative(fallback))
		}
		payload := routingdecision.DecisionPayload{
			Schema: routingdecision.SchemaVersion, DecisionID: "legacy-" + legacy.ID + "-" + source.Digest[:12], WorkBeadID: work.ID,
			WorkRevision: work.Revision, ClaimFence: work.ClaimFence, WorkStateDigest: stateDigest,
			City: legacy.City, Rig: legacy.Rig, Target: legacy.Target, TargetConfigDigest: targetDigest,
			PolicyDigest: strings.TrimPrefix(legacy.PolicyDigest, "sha256:"), ObservationDigest: strings.TrimPrefix(legacy.ObservationDigest, "sha256:"),
			Model: legacy.Model, Source: legacy.Source, Account: legacy.Account, ServeAs: legacy.ServeAs, Provider: legacy.Provider, Endpoint: legacy.Endpoint,
			Reason: legacy.Reason, Evidence: []string{"legacy_approval_id=" + legacy.ApprovalID}, Alternatives: alternatives,
			CreatedAt: legacy.CreatedAt, ExpiresAt: legacy.ExpiresAt, NoMigration: true,
		}
		payload.BindingID = routingdecision.BindingID(payload)
		payloads = append(payloads, payload)
	}
	decisionStore, err := routingdecision.OpenStore(cityPath, routingdecision.StoreOptions{})
	if err != nil {
		return routingdecision.LegacyImportResult{}, errors.New("ledger unavailable")
	}
	defer decisionStore.Close() //nolint:errcheck
	result, err := decisionStore.ImportLegacy(source, payloads)
	if err != nil {
		return routingdecision.LegacyImportResult{}, errors.New("legacy import failed")
	}
	return result, nil
}

func openExistingRoutingDecisionWorkStore(ctx context.Context, cityPath string, cfg *config.City, rigName string) (beads.Store, error) {
	scopeRoot := cityPath
	if rigName != "" {
		found := false
		for _, rig := range cfg.Rigs {
			if rig.Name == rigName && strings.TrimSpace(rig.Path) != "" {
				scopeRoot, found = rig.Path, true
				break
			}
		}
		if !found {
			return nil, errors.New("rig unavailable")
		}
	}
	provider := rawBeadsProviderForScope(resolveStoreScopeRoot(cityPath, scopeRoot), cityPath)
	switch {
	case provider == "file":
		return openExistingScopeLocalFileStore(resolveStoreScopeRoot(cityPath, scopeRoot))
	case providerUsesBdStoreContract(provider):
		if err := requireExistingExecutionReemitBdStore(resolveStoreScopeRoot(cityPath, scopeRoot)); err != nil {
			return nil, err
		}
		if rigName == "" {
			return scopedBdStoreForCity(ctx, cityPath)
		}
		return scopedBdStoreForRig(ctx, cityPath, cfg, scopeRoot)
	default:
		return nil, errors.New("offline work store provider unsupported")
	}
}

func containsExactReadyWork(items []beads.Bead, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
