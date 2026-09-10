package gastown_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/formula"
	"github.com/gastownhall/gascity/internal/orders"
)

func TestCandidateReviewRepairPackContractIsDiscoverable(t *testing.T) {
	root := exampleDir()
	orderData, err := os.ReadFile(filepath.Join(root, "orders", "candidate-review-repair.toml"))
	if err != nil {
		t.Fatal(err)
	}
	order, err := orders.Parse(orderData)
	if err != nil {
		t.Fatalf("parse candidate repair order: %v", err)
	}
	order.Name = "candidate-review-repair"
	if err := orders.Validate(order); err != nil {
		t.Fatalf("validate candidate repair order: %v", err)
	}
	if order.Scope != "rig" || order.Trigger != "cooldown" || order.Exec == "" {
		t.Fatalf("order = %#v, want rig-scoped cooldown exec order", order)
	}

	parser := formula.NewParser(filepath.Join(root, "formulas"))
	recipe, err := parser.ParseFile(filepath.Join(root, "formulas", "mol-candidate-review-repair.toml"))
	if err != nil {
		t.Fatalf("parse candidate repair formula: %v", err)
	}
	if recipe.Formula != "mol-candidate-review-repair" || len(recipe.Steps) != 1 {
		t.Fatalf("formula = %#v, want one candidate repair step", recipe)
	}
}

func TestCandidateReviewRepairOrderRoutesOnlyExplicitMechanicalHolds(t *testing.T) {
	root := exampleDir()
	orderScript := filepath.Join(root, "assets", "scripts", "candidate-review-repair.sh")
	fakeGC := writeCandidateRepairFakeGC(t)
	query := []map[string]any{
		{"id": "mechanical-1", "status": "open", "assignee": "rig/reviewer", "metadata": map[string]string{
			"gc.candidate_review_state":           "actionable",
			"gc.candidate_review_hold_class":      "mechanical",
			"gc.candidate_review_correction":      "remove the named residue",
			"gc.candidate_review_owner":           "rig/repairer",
			"gc.candidate_review_repair_route":    "rig/repairer",
			"gc.candidate_review_repair_workflow": "mol-candidate-review-repair",
			"gc.candidate_review_review_route":    "rig/reviewer",
			"gc.candidate_review_target":          "main",
			"gc.candidate_review_source":          "candidate",
			"gc.candidate_review_residue":         "[]",
			"gc.candidate_review_landed":          "[]",
			"gc.candidate_review_test_command":    "echo bead-injected-gate",
		}},
		{"id": "human-1", "status": "open", "assignee": "human", "metadata": map[string]string{
			"gc.candidate_review_state":      "actionable",
			"gc.candidate_review_hold_class": "human",
		}},
		{"id": "human-review-1", "status": "in_progress", "assignee": "human", "metadata": map[string]string{
			"gc.candidate_review_state":        "published_pending_review",
			"gc.candidate_review_hold_class":   "human",
			"gc.candidate_review_review_route": "rig/reviewer",
		}},
		{"id": "foreign-1", "status": "open", "assignee": "other/worker", "metadata": map[string]string{
			"gc.candidate_review_state":      "actionable",
			"gc.candidate_review_hold_class": "uncertain",
		}},
		{"id": "exhausted-1", "status": "open", "assignee": "rig/reviewer", "metadata": map[string]string{
			"gc.candidate_review_state":           "repair_failed",
			"gc.candidate_review_hold_class":      "mechanical",
			"gc.candidate_review_correction":      "bounded correction",
			"gc.candidate_review_owner":           "rig/repairer",
			"gc.candidate_review_repair_route":    "rig/repairer",
			"gc.candidate_review_repair_workflow": "mol-candidate-review-repair",
			"gc.candidate_review_review_route":    "rig/reviewer",
			"gc.candidate_review_target":          "main",
			"gc.candidate_review_source":          "candidate",
			"gc.candidate_review_residue":         "[]",
			"gc.candidate_review_landed":          "[]",
			"gc.candidate_review_repair_attempt":  "2",
			"gc.candidate_review_max_attempts":    "2",
			"gc.candidate_review_test_command":    "git diff --check",
		}},
		{"id": "stale-queued-1", "status": "in_progress", "assignee": "dead/repairer", "metadata": map[string]string{
			"gc.candidate_review_state":            "queued",
			"gc.candidate_review_hold_class":       "mechanical",
			"gc.candidate_review_correction":       "resume the exact correction",
			"gc.candidate_review_owner":            "rig/repairer",
			"gc.candidate_review_repair_route":     "rig/repairer",
			"gc.candidate_review_repair_workflow":  "mol-candidate-review-repair",
			"gc.candidate_review_review_route":     "rig/reviewer",
			"gc.candidate_review_target":           "main",
			"gc.candidate_review_source":           "candidate",
			"gc.candidate_review_residue":          "[]",
			"gc.candidate_review_landed":           "[]",
			"gc.candidate_review_test_command":     "git diff --check",
			"gc.candidate_review_repair_attempt":   "1",
			"gc.candidate_review_max_attempts":     "3",
			"gc.candidate_review_repair_queued_at": "2000-01-01T00:00:00Z",
		}},
		{"id": "fresh-active-1", "status": "in_progress", "assignee": "live/repairer", "metadata": map[string]string{
			"gc.candidate_review_state":          "active",
			"gc.candidate_review_hold_class":     "mechanical",
			"gc.candidate_review_started_at":     "2999-01-01T00:00:00Z",
			"gc.candidate_review_repair_attempt": "1",
		}},
	}
	queryJSON, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	queryFile := filepath.Join(t.TempDir(), "query.json")
	if err := os.WriteFile(queryFile, queryJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(t.TempDir(), "gc.log")
	env := append(os.Environ(),
		"PATH="+filepath.Dir(fakeGC)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GC_QUERY="+queryFile,
		"FAKE_GC_LOG="+logFile,
		"GC_PACK_STATE_DIR="+t.TempDir(),
		"PACK_DIR="+root,
	)
	if out, err := runCmdWithEnv(t, "", env, "bash", orderScript); err != nil {
		t.Fatalf("candidate repair order: %v\n%s", err, out)
	}
	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logData)
	if strings.Count(log, "sling rig/repairer mechanical-1") != 1 {
		t.Fatalf("mechanical hold was not routed exactly once; log:\n%s", log)
	}
	if !strings.Contains(log, "--var test_command=git diff --check") {
		t.Fatalf("configured gate was not passed to the repair formula; log:\n%s", log)
	}
	if strings.Contains(log, "bead-injected-gate") {
		t.Fatalf("bead metadata injected an unattended gate command; log:\n%s", log)
	}
	if strings.Contains(log, "--no-convoy") || strings.Contains(log, "--var bead_id=") {
		t.Fatalf("v2 repair formula bypassed its runtime convoy binding; log:\n%s", log)
	}
	if strings.Contains(log, "human-1") || strings.Contains(log, "human-review-1") || strings.Contains(log, "foreign-1") {
		t.Fatalf("non-mechanical holds were mutated; log:\n%s", log)
	}
	if strings.Contains(log, "sling rig/repairer exhausted-1") {
		t.Fatalf("exhausted hold was dispatched again; log:\n%s", log)
	}
	if strings.Count(log, "sling rig/repairer stale-queued-1") != 1 {
		t.Fatalf("stale queued hold was not recovered exactly once; log:\n%s", log)
	}
	if strings.Contains(log, "fresh-active-1") {
		t.Fatalf("fresh active hold was disturbed; log:\n%s", log)
	}
	if !strings.Contains(log, "exhausted-1") {
		t.Fatalf("exhausted hold did not record bounded terminal evidence; log:\n%s", log)
	}
}

func TestCandidateReviewRepairWorkerPublishesCurrentTargetCandidate(t *testing.T) {
	root := exampleDir()
	worker := filepath.Join(root, "assets", "scripts", "candidate-review-repair-worker.sh")
	fakeGC := writeCandidateRepairFakeGC(t)
	temp := t.TempDir()
	remote := filepath.Join(temp, "origin.git")
	seed := filepath.Join(temp, "seed")
	work := filepath.Join(temp, "work")
	runCandidateRepairGit(t, "init", "--bare", remote)
	runCandidateRepairGit(t, "init", "-b", "main", seed)
	runCandidateRepairGit(t, "-C", seed, "config", "user.email", "test@example.invalid")
	runCandidateRepairGit(t, "-C", seed, "config", "user.name", "Candidate Repair Test")
	writeCandidateRepairFile(t, filepath.Join(seed, "kept.txt"), "target\n")
	writeCandidateRepairFile(t, filepath.Join(seed, "overlap.md"), "target\n")
	runCandidateRepairGit(t, "-C", seed, "add", "kept.txt", "overlap.md")
	runCandidateRepairGit(t, "-C", seed, "commit", "-m", "target")
	runCandidateRepairGit(t, "-C", seed, "remote", "add", "origin", remote)
	runCandidateRepairGit(t, "-C", seed, "push", "origin", "main")
	// The bare remote's default HEAD is still the Git default branch, while
	// this fixture intentionally publishes only main and candidate. Clone the
	// intended base explicitly so candidate is a descendant of main rather
	// than an unrelated root that a real repair must reject.
	runCandidateRepairGit(t, "clone", "--branch", "main", remote, work)
	runCandidateRepairGit(t, "-C", work, "config", "user.email", "test@example.invalid")
	runCandidateRepairGit(t, "-C", work, "config", "user.name", "Candidate Repair Test")
	runCandidateRepairGit(t, "-C", work, "switch", "-c", "candidate")
	writeCandidateRepairFile(t, filepath.Join(work, "fix.txt"), "fixed\n")
	writeCandidateRepairFile(t, filepath.Join(work, "agent-plan.md"), "agent residue\n")
	writeCandidateRepairFile(t, filepath.Join(work, "overlap.md"), "candidate overlap\n")
	runCandidateRepairGit(t, "-C", work, "add", "fix.txt", "agent-plan.md", "overlap.md")
	runCandidateRepairGit(t, "-C", work, "commit", "-m", "candidate")
	runCandidateRepairGit(t, "-C", work, "push", "origin", "candidate")
	lockRoot := filepath.Join(work, ".git", "gc-candidate-review-locks")
	if err := os.MkdirAll(lockRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	writeCandidateRepairFile(t, filepath.Join(lockRoot, "writer"), host+"\n99999999\nstale-token\n")
	writeCandidateRepairFile(t, filepath.Join(seed, "new-target.txt"), "current target\n")
	runCandidateRepairGit(t, "-C", seed, "add", "new-target.txt")
	runCandidateRepairGit(t, "-C", seed, "commit", "-m", "advance target")
	runCandidateRepairGit(t, "-C", seed, "push", "origin", "main")
	target := strings.TrimSpace(runCandidateRepairGit(t, "-C", seed, "rev-parse", "HEAD"))
	beadFile := filepath.Join(temp, "bead.json")
	bead := map[string]any{"id": "candidate-1", "status": "in_progress", "assignee": "rig/repairer", "metadata": map[string]string{
		"gc.candidate_review_state":          "queued",
		"gc.candidate_review_hold_class":     "mechanical",
		"gc.candidate_review_correction":     "apply the requested correction before invoking the worker",
		"gc.candidate_review_owner":          "rig/repairer",
		"gc.candidate_review_repair_route":   "rig/repairer",
		"gc.candidate_review_target":         "main",
		"gc.candidate_review_source":         "candidate",
		"gc.candidate_review_residue":        `["agent-plan.md"]`,
		"gc.candidate_review_landed":         `["overlap.md"]`,
		"gc.candidate_review_review_route":   "rig/reviewer",
		"gc.candidate_review_token":          "repair-token-1",
		"gc.candidate_review_repair_attempt": "1",
		"gc.candidate_review_max_attempts":   "3",
		"gc.work_dir":                        filepath.Join(temp, "untrusted-bead-work-dir"),
		"gc.work_branch":                     "candidate",
	}}
	beadJSON, err := json.Marshal([]any{bead})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beadFile, beadJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(temp, "gc.log")
	env := append(os.Environ(),
		"PATH="+filepath.Dir(fakeGC)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GC_BEAD="+beadFile,
		"FAKE_GC_LOG="+logFile,
		"GC_AGENT=rig/repairer",
		"GC_CANDIDATE_REPAIR_TOKEN=repair-token-1",
		"GC_CANDIDATE_REPAIR_WORK_DIR="+work,
		"GC_CANDIDATE_REPAIR_TEST_COMMAND=git diff --check",
	)
	if out, err := runCmdWithEnv(t, work, env, "bash", worker, "candidate-1"); err != nil {
		t.Fatalf("candidate repair worker: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(work, "agent-plan.md")); !os.IsNotExist(err) {
		t.Fatalf("worker retained explicitly declared residue: err=%v", err)
	}
	if got := string(readCandidateRepairFile(t, filepath.Join(work, "overlap.md"))); got != "target\n" {
		t.Fatalf("overlap.md = %q, want target content", got)
	}
	branchSHA := strings.TrimSpace(runCandidateRepairGit(t, "-C", work, "rev-parse", "candidate"))
	if branchSHA == target {
		t.Fatal("worker did not publish a candidate commit")
	}
	if _, err := runCmdWithEnv(t, work, nil, "git", "-C", work, "merge-base", "--is-ancestor", target, "candidate"); err != nil {
		t.Fatalf("current target %s is not an ancestor of repaired candidate", target)
	}
	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "sling rig/reviewer candidate-1") {
		t.Fatalf("worker did not resubmit review; log:\n%s", logData)
	}
}

func TestCandidateReviewRepairOrderDoesNotDispatchAfterLosingClaimCAS(t *testing.T) {
	root := exampleDir()
	fakeGC := writeCandidateRepairFakeGC(t)
	query := []map[string]any{{"id": "race-1", "status": "open", "assignee": "rig/reviewer", "metadata": map[string]string{
		"gc.candidate_review_state":           "actionable",
		"gc.candidate_review_hold_class":      "mechanical",
		"gc.candidate_review_correction":      "exact correction",
		"gc.candidate_review_owner":           "rig/repairer",
		"gc.candidate_review_repair_route":    "rig/repairer",
		"gc.candidate_review_repair_workflow": "mol-candidate-review-repair",
		"gc.candidate_review_review_route":    "rig/reviewer",
		"gc.candidate_review_target":          "main",
		"gc.candidate_review_source":          "candidate",
		"gc.candidate_review_residue":         "[]",
		"gc.candidate_review_landed":          "[]",
		"gc.candidate_review_test_command":    "git diff --check",
	}}}
	queryJSON, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	queryFile := filepath.Join(t.TempDir(), "query.json")
	logFile := filepath.Join(t.TempDir(), "gc.log")
	if err := os.WriteFile(queryFile, queryJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"PATH="+filepath.Dir(fakeGC)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GC_QUERY="+queryFile,
		"FAKE_GC_LOG="+logFile,
		"FAKE_GC_UPDATE_EXIT=13",
		"GC_PACK_STATE_DIR="+t.TempDir(),
		"PACK_DIR="+root,
	)
	if out, err := runCmdWithEnv(t, "", env, "bash", filepath.Join(root, "assets", "scripts", "candidate-review-repair.sh")); err != nil {
		t.Fatalf("losing a claim race should be a clean no-op: %v\n%s", err, out)
	}
	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "sling ") {
		t.Fatalf("losing CAS contender dispatched duplicate work; log:\n%s", logData)
	}
}

func TestCandidateReviewRepairWorkerPreservesUncertainWork(t *testing.T) {
	t.Run("dirty worktree", func(t *testing.T) {
		work := t.TempDir()
		runCandidateRepairGit(t, "init", "-b", "candidate", work)
		writeCandidateRepairFile(t, filepath.Join(work, "foreign.txt"), "do not touch\n")
		assertCandidateRepairWorkerFailsClosed(t, work, "candidate", `["foreign.txt"]`, "worktree is dirty", "foreign.txt")
	})
	t.Run("path traversal", func(t *testing.T) {
		work := t.TempDir()
		runCandidateRepairGit(t, "init", "-b", "candidate", work)
		assertCandidateRepairWorkerFailsClosed(t, work, "candidate", `["../outside.txt"]`, "invalid", "")
	})
	t.Run("newline path splitting", func(t *testing.T) {
		work := t.TempDir()
		runCandidateRepairGit(t, "init", "-b", "candidate", work)
		assertCandidateRepairWorkerFailsClosed(t, work, "candidate", `["safe\n/var/tmp/outside"]`, "control-character", "")
	})
	t.Run("nested work directory", func(t *testing.T) {
		work := t.TempDir()
		runCandidateRepairGit(t, "init", "-b", "candidate", work)
		nested := filepath.Join(work, "nested")
		if err := os.Mkdir(nested, 0o700); err != nil {
			t.Fatal(err)
		}
		assertCandidateRepairWorkerFailsClosed(t, nested, "candidate", `[]`, "Git top-level", "")
	})
	t.Run("source equals target", func(t *testing.T) {
		work := t.TempDir()
		runCandidateRepairGit(t, "init", "-b", "main", work)
		assertCandidateRepairWorkerFailsClosed(t, work, "main", `[]`, "source and target must be distinct", "")
	})
	t.Run("live repository writer lock", func(t *testing.T) {
		work := t.TempDir()
		runCandidateRepairGit(t, "init", "-b", "candidate", work)
		lockRoot := filepath.Join(work, ".git", "gc-candidate-review-locks")
		if err := os.MkdirAll(lockRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		host, err := os.Hostname()
		if err != nil {
			t.Fatal(err)
		}
		writeCandidateRepairFile(t, filepath.Join(lockRoot, "writer"), fmt.Sprintf("%s\n%d\nlive-token\n", host, os.Getpid()))
		assertCandidateRepairWorkerFailsClosed(t, work, "candidate", `[]`, "another repair writer owns", "")
	})
	t.Run("symlink parent escape", func(t *testing.T) {
		work := t.TempDir()
		outside := t.TempDir()
		runCandidateRepairGit(t, "init", "-b", "candidate", work)
		runCandidateRepairGit(t, "-C", work, "config", "user.email", "test@example.invalid")
		runCandidateRepairGit(t, "-C", work, "config", "user.name", "Candidate Repair Test")
		if err := os.Symlink(outside, filepath.Join(work, "escape")); err != nil {
			t.Fatal(err)
		}
		runCandidateRepairGit(t, "-C", work, "add", "escape")
		runCandidateRepairGit(t, "-C", work, "commit", "-m", "tracked symlink")
		outsideFile := filepath.Join(outside, "outside.txt")
		writeCandidateRepairFile(t, outsideFile, "preserve\n")
		assertCandidateRepairWorkerFailsClosed(t, work, "candidate", `["escape/outside.txt"]`, "symlink", "")
		if got := string(readCandidateRepairFile(t, outsideFile)); got != "preserve\n" {
			t.Fatalf("symlink escape target changed: %q", got)
		}
	})
}

func TestCandidateReviewRepairWorkerDoesNotMutateReclassifiedHold(t *testing.T) {
	root := exampleDir()
	fakeGC := writeCandidateRepairFakeGC(t)
	temp := t.TempDir()
	beadFile := filepath.Join(temp, "bead.json")
	beadJSON, err := json.Marshal([]any{map[string]any{
		"id": "human-1", "status": "in_progress", "assignee": "rig/repairer",
		"metadata": map[string]string{
			"gc.candidate_review_state":      "queued",
			"gc.candidate_review_hold_class": "human",
			"gc.candidate_review_token":      "repair-token-human",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beadFile, beadJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(temp, "gc.log")
	env := append(os.Environ(),
		"PATH="+filepath.Dir(fakeGC)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GC_BEAD="+beadFile,
		"FAKE_GC_LOG="+logFile,
		"GC_AGENT=rig/repairer",
		"GC_CANDIDATE_REPAIR_TOKEN=repair-token-human",
	)
	if out, err := runCmdWithEnv(t, "", env, "bash", filepath.Join(root, "assets", "scripts", "candidate-review-repair-worker.sh"), "human-1"); err == nil {
		t.Fatalf("worker accepted a reclassified human hold; output:\n%s", out)
	}
	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "bd update") || strings.Contains(string(logData), "sling ") {
		t.Fatalf("worker mutated or routed a reclassified hold; log:\n%s", logData)
	}
}

func assertCandidateRepairWorkerFailsClosed(t *testing.T, work, source, residue, want, dirtyPath string) {
	t.Helper()
	worker := filepath.Join(exampleDir(), "assets", "scripts", "candidate-review-repair-worker.sh")
	fakeGC := writeCandidateRepairFakeGC(t)
	temp := t.TempDir()
	beadFile := filepath.Join(temp, "bead.json")
	metadata := map[string]string{
		"gc.candidate_review_state":          "queued",
		"gc.candidate_review_hold_class":     "mechanical",
		"gc.candidate_review_owner":          "rig/repairer",
		"gc.candidate_review_repair_route":   "rig/repairer",
		"gc.candidate_review_review_route":   "rig/reviewer",
		"gc.candidate_review_target":         "main",
		"gc.candidate_review_source":         source,
		"gc.candidate_review_residue":        residue,
		"gc.candidate_review_landed":         "[]",
		"gc.candidate_review_token":          "repair-token-fail",
		"gc.candidate_review_repair_attempt": "1",
		"gc.candidate_review_max_attempts":   "3",
		"gc.work_dir":                        work,
	}
	beadJSON, err := json.Marshal([]any{map[string]any{
		"id": "candidate-fail-closed", "status": "in_progress", "assignee": "rig/repairer", "metadata": metadata,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beadFile, beadJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(temp, "gc.log")
	env := append(os.Environ(),
		"PATH="+filepath.Dir(fakeGC)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GC_BEAD="+beadFile,
		"FAKE_GC_LOG="+logFile,
		"GC_AGENT=rig/repairer",
		"GC_CANDIDATE_REPAIR_TOKEN=repair-token-fail",
		"GC_CANDIDATE_REPAIR_WORK_DIR="+work,
	)
	out, err := runCmdWithEnv(t, work, env, "bash", worker, "candidate-fail-closed")
	if err == nil {
		t.Fatalf("worker succeeded for unsafe candidate; output=%s", out)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("worker error = %q, want %q", out, want)
	}
	if dirtyPath != "" {
		if _, err := os.Stat(filepath.Join(work, dirtyPath)); err != nil {
			t.Fatalf("foreign work was removed: %v", err)
		}
	}
	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "sling ") {
		t.Fatalf("unsafe candidate was routed after fail-closed check; log:\n%s", logData)
	}
}

func writeCandidateRepairFakeGC(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gc")
	script := `#!/bin/sh
set -eu
log="${FAKE_GC_LOG:?}"
printf '%s\n' "gc $*" >> "$log"
if [ "$1" = "bd" ] && [ "$2" = "query" ]; then
  cat "${FAKE_GC_QUERY:?}"
  exit 0
fi
if [ "$1" = "bd" ] && [ "$2" = "show" ]; then
  cat "${FAKE_GC_BEAD:?}"
  exit 0
fi
if [ "$1" = "bd" ] && [ "$2" = "update" ] && [ -n "${FAKE_GC_UPDATE_EXIT:-}" ]; then
  exit "$FAKE_GC_UPDATE_EXIT"
fi
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCandidateRepairGit(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runCmdWithEnv(t, "", nil, "git", args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeCandidateRepairFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readCandidateRepairFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(fmt.Errorf("read %s: %w", path, err))
	}
	return data
}
