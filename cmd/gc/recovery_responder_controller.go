package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/pathutil"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

// reconcileRecoveryResponder is a controller adapter only: worker owns the
// incident/work transition, session owns its metadata, and the existing work
// controller owns claiming and session lifecycle.
func (cr *CityRuntime) reconcileRecoveryResponder(ctx context.Context, now time.Time) map[string]any {
	if cr == nil || cr.cfg == nil || cr.cfg.RecoveryResponder == nil {
		return nil
	}
	cfg := cr.cfg.RecoveryResponder
	advisor, advisorErr := recoveryAdvisorForConfig(cr.cityPath, cfg, cr.cfg)
	if advisorErr != nil {
		// Advisory failure is fail-open by design: configured target order remains
		// the deterministic recovery path.
		fmt.Fprintf(cr.stderr, "%s: recovery responder: Wayfinder advisory disabled: %v\n", cr.logPrefix, advisorErr) //nolint:errcheck
	}
	responder := worker.NewRecoveryResponder(
		sessionpkg.NewStore(cr.sessionsBeadStore()),
		cr.cityWorkStore().Store,
		worker.RecoveryResponderOptions{
			Targets: cfg.Targets, HoldDuration: cfg.HoldDuration(),
			AdvisoryTimeout: cfg.AdvisoryTimeoutDuration(), Cooldown: cfg.CooldownDuration(),
			MaxAttempts: cfg.MaxAttempts, Advisor: advisor,
		},
	)
	report, err := responder.Reconcile(ctx, now)
	if err != nil {
		fmt.Fprintf(cr.stderr, "%s: recovery responder: %v\n", cr.logPrefix, err) //nolint:errcheck
	}
	return map[string]any{
		"created": report.Created, "active": report.Active,
		"cooling_down": report.CoolingDown, "exhausted": report.Exhausted,
	}
}

func recoveryAdvisorForConfig(cityPath string, cfg *config.RecoveryResponderConfig, cityCfg *config.City) (worker.RecoveryAdvisor, error) {
	if cfg == nil || strings.TrimSpace(cfg.WayfinderURL) == "" {
		return nil, nil
	}
	accountRefs := installedRecoveryAccountRefs(cityCfg)
	if len(accountRefs) == 0 {
		return nil, fmt.Errorf("no configured Wayfinder account resolves to a same-named installed executable")
	}
	_, _, _, err := recoveryWayfinderRequestCandidate(cityPath, cfg.WayfinderRequestFile)
	if err != nil {
		return nil, err
	}
	return fileRecoveryAdvisor{
		baseURL:               cfg.WayfinderURL,
		cityPath:              cityPath,
		configuredRequestPath: cfg.WayfinderRequestFile,
		accountRefs:           accountRefs,
	}, nil
}

// installedRecoveryAccountRefs admits only explicit catalog identities backed
// by a same-named executable. ResolveProvider alone permits arbitrary aliases,
// path_check indirection, and unchecked workspace start_command overrides.
// This is executable admission, NOT credential/authentication or backend-account
// attestation: wrappers and front ends remain operator-owned trust boundaries.
func installedRecoveryAccountRefs(cityCfg *config.City) []string {
	if cityCfg == nil || len(cityCfg.Providers) == 0 {
		return nil
	}
	refs := make([]string, 0, len(cityCfg.Providers))
	for name := range cityCfg.Providers {
		resolved, err := config.ResolveProvider(
			&config.Agent{Provider: name}, &cityCfg.Workspace, cityCfg.Providers, exec.LookPath,
		)
		if err != nil || resolved == nil {
			continue
		}
		// No shell parsing or identifier normalization. Absolute paths are local
		// only; their basename must match the exact operator-declared identity.
		command := resolved.Command
		if command != name && (!filepath.IsAbs(command) || filepath.Base(command) != name) {
			continue
		}
		if _, err := exec.LookPath(command); err != nil {
			continue
		}
		refs = append(refs, name)
	}
	sort.Strings(refs)
	return refs
}

type fileRecoveryAdvisor struct {
	baseURL               string
	cityPath              string
	configuredRequestPath string
	accountRefs           []string
}

func (a fileRecoveryAdvisor) Recommend(ctx context.Context, request worker.RecoveryRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, requestPath, raw, err := recoveryWayfinderRequestCandidate(a.cityPath, a.configuredRequestPath)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	relativePath, err := filepath.Rel(root, requestPath)
	if err != nil {
		return "", fmt.Errorf("relativize Wayfinder request template %q: %w", raw, err)
	}
	template, err := readRecoveryWayfinderTemplateAt(root, relativePath)
	if err != nil {
		return "", fmt.Errorf("read Wayfinder request template %q: %w", raw, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	advisor, err := worker.NewWayfinderRecoveryAdvisor(a.baseURL, template, a.accountRefs)
	if err != nil {
		return "", err
	}
	return advisor.Recommend(ctx, request)
}

func recoveryWayfinderRequestPath(cityPath, configured string) (string, error) {
	root, candidate, raw, err := recoveryWayfinderRequestCandidate(cityPath, configured)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve city root for Wayfinder request template: %w", err)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve Wayfinder request template %q: %w", raw, err)
	}
	resolvedRoot = pathutil.NormalizePathForCompare(resolvedRoot)
	resolvedCandidate = pathutil.NormalizePathForCompare(resolvedCandidate)
	if !pathutil.PathWithin(resolvedRoot, resolvedCandidate) {
		return "", fmt.Errorf("wayfinder request template %q resolves outside city root", raw)
	}
	return resolvedCandidate, nil
}

func recoveryWayfinderRequestCandidate(cityPath, configured string) (root, candidate, raw string, err error) {
	root = pathutil.NormalizePathForCompare(strings.TrimSpace(cityPath))
	if root == "" {
		return "", "", "", fmt.Errorf("cannot resolve Wayfinder request template without city root")
	}
	raw = strings.TrimSpace(configured)
	if raw == "" {
		return "", "", "", fmt.Errorf("wayfinder_request_file is required")
	}
	candidate = raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = pathutil.NormalizePathForCompare(candidate)
	if !pathutil.PathWithin(root, candidate) {
		return "", "", "", fmt.Errorf("wayfinder request template %q is outside city root", raw)
	}
	return root, candidate, raw, nil
}
