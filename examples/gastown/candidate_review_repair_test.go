package gastown_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
		}},
		{"id": "human-1", "status": "open", "assignee": "human", "metadata": map[string]string{
			"gc.candidate_review_state":      "actionable",
			"gc.candidate_review_hold_class": "human",
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
	cmd := exec.Command("bash", orderScript)
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Dir(fakeGC)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GC_QUERY="+queryFile,
		"FAKE_GC_LOG="+logFile,
		"GC_PACK_STATE_DIR="+t.TempDir(),
		"PACK_DIR="+root,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
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
	if strings.Contains(log, "human-1") || strings.Contains(log, "foreign-1") {
		t.Fatalf("non-mechanical holds were mutated; log:\n%s", log)
	}
	if strings.Contains(log, "sling rig/repairer exhausted-1") {
		t.Fatalf("exhausted hold was dispatched again; log:\n%s", log)
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
	writeCandidateRepairFile(t, filepath.Join(seed, "new-target.txt"), "current target\n")
	runCandidateRepairGit(t, "-C", seed, "add", "new-target.txt")
	runCandidateRepairGit(t, "-C", seed, "commit", "-m", "advance target")
	runCandidateRepairGit(t, "-C", seed, "push", "origin", "main")
	target := strings.TrimSpace(runCandidateRepairGit(t, "-C", seed, "rev-parse", "HEAD"))
	beadFile := filepath.Join(temp, "bead.json")
	bead := map[string]any{"id": "candidate-1", "status": "in_progress", "assignee": "rig/repairer", "metadata": map[string]string{
		"gc.candidate_review_state":        "queued",
		"gc.candidate_review_hold_class":   "mechanical",
		"gc.candidate_review_correction":   "apply the requested correction before invoking the worker",
		"gc.candidate_review_owner":        "rig/repairer",
		"gc.candidate_review_repair_route": "rig/repairer",
		"gc.candidate_review_target":       "main",
		"gc.candidate_review_source":       "candidate",
		"gc.candidate_review_residue":      `["agent-plan.md"]`,
		"gc.candidate_review_landed":       `["overlap.md"]`,
		"gc.candidate_review_review_route": "rig/reviewer",
		"gc.work_dir":                      work,
		"gc.work_branch":                   "candidate",
	}}
	beadJSON, err := json.Marshal([]any{bead})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beadFile, beadJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(temp, "gc.log")
	cmd := exec.Command("bash", worker, "candidate-1")
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Dir(fakeGC)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GC_BEAD="+beadFile,
		"FAKE_GC_LOG="+logFile,
		"GC_AGENT=rig/repairer",
		"GC_CANDIDATE_REPAIR_TEST_COMMAND=git diff --check",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
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
	if cmd := exec.Command("git", "-C", work, "merge-base", "--is-ancestor", target, "candidate"); cmd.Run() != nil {
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

func TestCandidateReviewRepairWorkerPreservesUncertainWork(t *testing.T) {
	t.Run("dirty worktree", func(t *testing.T) {
		work := t.TempDir()
		runCandidateRepairGit(t, "init", "-b", "main", work)
		writeCandidateRepairFile(t, filepath.Join(work, "foreign.txt"), "do not touch\n")
		assertCandidateRepairWorkerFailsClosed(t, work, `["foreign.txt"]`, "worktree is dirty", "foreign.txt")
	})
	t.Run("path traversal", func(t *testing.T) {
		work := t.TempDir()
		runCandidateRepairGit(t, "init", "-b", "main", work)
		assertCandidateRepairWorkerFailsClosed(t, work, `["../outside.txt"]`, "invalid absolute", "")
	})
}

func assertCandidateRepairWorkerFailsClosed(t *testing.T, work, residue, wantError, preservePath string) {
	t.Helper()
	worker := filepath.Join(exampleDir(), "assets", "scripts", "candidate-review-repair-worker.sh")
	fakeGC := writeCandidateRepairFakeGC(t)
	temp := t.TempDir()
	beadFile := filepath.Join(temp, "bead.json")
	metadata := map[string]string{
		"gc.candidate_review_state":        "queued",
		"gc.candidate_review_hold_class":   "mechanical",
		"gc.candidate_review_owner":        "rig/repairer",
		"gc.candidate_review_repair_route": "rig/repairer",
		"gc.candidate_review_review_route": "rig/reviewer",
		"gc.candidate_review_target":       "main",
		"gc.candidate_review_source":       "candidate",
		"gc.candidate_review_residue":      residue,
		"gc.candidate_review_landed":       "[]",
		"gc.work_dir":                      work,
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
	cmd := exec.Command("bash", worker, "candidate-fail-closed")
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Dir(fakeGC)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GC_BEAD="+beadFile,
		"FAKE_GC_LOG="+logFile,
		"GC_AGENT=rig/repairer",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("worker succeeded for unsafe candidate; output=%s", out)
	}
	if !strings.Contains(string(out), wantError) {
		t.Fatalf("worker error = %q, want %q", out, wantError)
	}
	if preservePath != "" {
		if _, err := os.Stat(filepath.Join(work, preservePath)); err != nil {
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
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCandidateRepairGit(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
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
