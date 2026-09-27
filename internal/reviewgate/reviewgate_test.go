package reviewgate

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestCheckKeepsOrdinaryClosedDependenciesEligible(t *testing.T) {
	got := Check(beads.Bead{Status: "closed"}, "candidate-1")
	if !got.Eligible {
		t.Fatalf("ordinary closed dependency rejected: %+v", got)
	}
}

func TestCheckRequiresClosedCanonicalApprovalForExactCandidate(t *testing.T) {
	base := beads.Bead{Status: "closed", Metadata: map[string]string{
		SchemaMetadataKey:    SchemaVersion,
		VerdictMetadataKey:   VerdictApprove,
		CandidateMetadataKey: "candidate-1",
	}}
	clone := func(b beads.Bead) beads.Bead {
		copy := b
		copy.Metadata = make(map[string]string, len(b.Metadata))
		for key, value := range b.Metadata {
			copy.Metadata[key] = value
		}
		return copy
	}
	tests := []struct {
		name      string
		bead      beads.Bead
		candidate string
		want      bool
	}{
		{name: "exact", bead: base, candidate: "candidate-1", want: true},
		{name: "mismatch", bead: base, candidate: "candidate-2"},
		{name: "open approval", bead: func() beads.Bead { b := clone(base); b.Status = "open"; return b }(), candidate: "candidate-1"},
		{name: "missing candidate", bead: func() beads.Bead { b := clone(base); delete(b.Metadata, CandidateMetadataKey); return b }(), candidate: "candidate-1"},
		{name: "candidate whitespace", bead: func() beads.Bead { b := clone(base); b.Metadata[CandidateMetadataKey] = " candidate-1"; return b }(), candidate: "candidate-1"},
		{name: "invalid verdict", bead: func() beads.Bead { b := clone(base); b.Metadata[VerdictMetadataKey] = "PASS"; return b }(), candidate: "candidate-1"},
		{name: "unsupported schema", bead: func() beads.Bead { b := clone(base); b.Metadata[SchemaMetadataKey] = "2"; return b }(), candidate: "candidate-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Check(tt.bead, tt.candidate).Eligible; got != tt.want {
				t.Fatalf("eligible = %t, want %t; result=%+v", got, tt.want, Check(tt.bead, tt.candidate))
			}
		})
	}
}

func TestCheckKeepsClosedBlockBlockingIncludingLegacyMarker(t *testing.T) {
	for _, metadata := range []map[string]string{
		{VerdictMetadataKey: VerdictBlock, SchemaMetadataKey: SchemaVersion, CandidateMetadataKey: "candidate-1"},
		{"review_verdict": VerdictBlock},
		{"verdict": VerdictBlock},
	} {
		if got := Check(beads.Bead{Status: "closed", Metadata: metadata}, "candidate-1"); got.Eligible {
			t.Fatalf("BLOCK dependency became eligible: metadata=%v result=%+v", metadata, got)
		}
	}
}
