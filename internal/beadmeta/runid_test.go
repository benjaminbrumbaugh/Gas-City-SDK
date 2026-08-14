package beadmeta

import "testing"

func TestResolveRunID(t *testing.T) {
	cases := []struct {
		name       string
		metadata   map[string]string
		selfID     string
		fallbackID string
		want       string
	}{
		{
			name:       "workflow_id wins (graph workflow)",
			metadata:   map[string]string{"workflow_id": "wf-1", "molecule_id": "mol-1", RootBeadIDMetadataKey: "root-1"},
			selfID:     "b1",
			fallbackID: "s1",
			want:       "wf-1",
		},
		{
			name:       "molecule_id next (poured/wisp)",
			metadata:   map[string]string{"molecule_id": "mol-1", RootBeadIDMetadataKey: "root-1"},
			selfID:     "b1",
			fallbackID: "s1",
			want:       "mol-1",
		},
		{
			name:       "gc.root_bead_id next (nested)",
			metadata:   map[string]string{RootBeadIDMetadataKey: "root-1"},
			selfID:     "b1",
			fallbackID: "s1",
			want:       "root-1",
		},
		{
			name:       "self id fallback (plain work bead, worker path)",
			metadata:   nil,
			selfID:     "b1",
			fallbackID: "s1",
			want:       "b1",
		},
		{
			name:       "final fallback (manual chat: no bead, session id)",
			metadata:   nil,
			selfID:     "",
			fallbackID: "s1",
			want:       "s1",
		},
		{
			name:       "compute path: empty final fallback yields self id",
			metadata:   nil,
			selfID:     "session-bead-9",
			fallbackID: "",
			want:       "session-bead-9",
		},
		{
			name:       "blank chain values are skipped",
			metadata:   map[string]string{"workflow_id": "  ", "molecule_id": "mol-3"},
			selfID:     "b1",
			fallbackID: "s1",
			want:       "mol-3",
		},
		{
			name:       "blank self id falls through to fallback",
			metadata:   nil,
			selfID:     "   ",
			fallbackID: "s1",
			want:       "s1",
		},
		{
			name:       "all empty yields empty",
			metadata:   map[string]string{},
			selfID:     "",
			fallbackID: "",
			want:       "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveRunID(tc.metadata, tc.selfID, tc.fallbackID); got != tc.want {
				t.Fatalf("ResolveRunID(%v, %q, %q) = %q, want %q", tc.metadata, tc.selfID, tc.fallbackID, got, tc.want)
			}
		})
	}
}

func TestResolveRunIdentityReportsTheWinningSource(t *testing.T) {
	cases := []struct {
		name       string
		metadata   map[string]string
		selfID     string
		fallbackID string
		wantID     string
		wantSource RunIdentityKind
	}{
		{"workflow", map[string]string{"workflow_id": "wf-1", MoleculeIDMetadataKey: "mol-1"}, "self", "session", "wf-1", RunIdentityWorkflow},
		{"molecule", map[string]string{MoleculeIDMetadataKey: "mol-1"}, "self", "session", "mol-1", RunIdentityMolecule},
		{"root", map[string]string{RootBeadIDMetadataKey: "root-1"}, "self", "session", "root-1", RunIdentityRootBead},
		{"self", nil, "bead-1", "session", "bead-1", RunIdentitySelfBead},
		{"session fallback", nil, "", "session-1", "session-1", RunIdentitySessionFallback},
		{"unknown", nil, "", "", "", RunIdentityUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveRunIdentity(tc.metadata, tc.selfID, tc.fallbackID)
			if got.ID != tc.wantID || got.Source != tc.wantSource {
				t.Fatalf("ResolveRunIdentity() = %+v, want ID=%q source=%q", got, tc.wantID, tc.wantSource)
			}
		})
	}
}
