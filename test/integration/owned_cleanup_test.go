//go:build integration

package integration

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestOwnedIntegrationKillSetPreservesForeignCanaries(t *testing.T) {
	ownedRoot := filepath.Join(t.TempDir(), "gc-integration-123-owned")
	script := "/checkout/test/agents/graph-dispatch.sh"
	procs := map[int]procSnapshot{
		101: {pid: 101, ppid: 1, cmd: ownedRoot + "/bin/gc supervisor run"},
		102: {pid: 102, ppid: 101, cmd: "sh " + script},
		201: {pid: 201, ppid: 1, cmd: "/foreign/gc-integration-999999-missing/bin/gc supervisor run"},
		202: {pid: 202, ppid: 201, cmd: "sh " + script},
		301: {pid: 301, ppid: 1, cmd: "/live/gc-integration-999999-present/bin/gc supervisor run"},
		401: {pid: 401, ppid: 1, cmd: "bd ready --assignee=worker --json --limit=1"},
		501: {pid: 501, ppid: 1, cmd: "sh " + script},
		601: {pid: 601, ppid: 1, cmd: ownedRoot + "-adjacent/bin/gc supervisor run"},
	}
	got := subprocessTestKillSet(procs, ownedRoot)
	if want := map[int]bool{101: true, 102: true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kill set = %v, want owned only %v", got, want)
	}
	if got := subprocessTestKillSet(procs, ""); len(got) != 0 {
		t.Fatalf("empty authority killed: %v", got)
	}
}
