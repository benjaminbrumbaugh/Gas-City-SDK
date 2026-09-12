package main

import (
	"context"
	"fmt"
	"path/filepath"
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
	advisor, advisorErr := recoveryAdvisorForConfig(cr.cityPath, cfg)
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

func recoveryAdvisorForConfig(cityPath string, cfg *config.RecoveryResponderConfig) (worker.RecoveryAdvisor, error) {
	if cfg == nil || strings.TrimSpace(cfg.WayfinderURL) == "" {
		return nil, nil
	}
	_, _, _, err := recoveryWayfinderRequestCandidate(cityPath, cfg.WayfinderRequestFile)
	if err != nil {
		return nil, err
	}
	return fileRecoveryAdvisor{
		baseURL:               cfg.WayfinderURL,
		cityPath:              cityPath,
		configuredRequestPath: cfg.WayfinderRequestFile,
	}, nil
}

type fileRecoveryAdvisor struct {
	baseURL               string
	cityPath              string
	configuredRequestPath string
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
	advisor, err := worker.NewWayfinderRecoveryAdvisor(a.baseURL, template)
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
