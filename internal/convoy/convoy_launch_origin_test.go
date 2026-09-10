package convoy

import (
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

func TestLaunchOriginKeyIsWritableByEveryMetadataRoute(t *testing.T) {
	if err := beadmeta.ValidateKey(LaunchOriginMetadataKey); err != nil {
		t.Fatalf("launch origin key must be accepted by every metadata write route: %v", err)
	}
}

func TestApplyConvoyFieldsStampsLaunchOrigin(t *testing.T) {
	b := beads.Bead{}
	ApplyConvoyFields(&b, ConvoyFields{LaunchOrigin: "actor-token"})

	if got := b.Metadata[LaunchOriginMetadataKey]; got != "actor-token" {
		t.Fatalf("%s = %q, want %q", LaunchOriginMetadataKey, got, "actor-token")
	}
}

// An absent origin must leave the bead byte-identical to the pre-capture shape,
// so a launch with no trustworthy actor route keeps its legacy behavior.
func TestApplyConvoyFieldsOmitsAnAbsentLaunchOrigin(t *testing.T) {
	withOrigin := beads.Bead{}
	ApplyConvoyFields(&withOrigin, ConvoyFields{Owner: "owner-token"})

	legacy := beads.Bead{}
	ApplyConvoyFields(&legacy, ConvoyFields{Owner: "owner-token", LaunchOrigin: ""})

	if !reflect.DeepEqual(withOrigin.Metadata, legacy.Metadata) {
		t.Fatalf("empty launch origin changed the bead: %#v vs %#v", legacy.Metadata, withOrigin.Metadata)
	}
	if _, ok := legacy.Metadata[LaunchOriginMetadataKey]; ok {
		t.Fatalf("%s must be absent, not empty, when no origin was captured", LaunchOriginMetadataKey)
	}
}

func TestGetConvoyFieldsReadsLaunchOrigin(t *testing.T) {
	b := beads.Bead{Metadata: map[string]string{LaunchOriginMetadataKey: "actor-token"}}

	if got := GetConvoyFields(b).LaunchOrigin; got != "actor-token" {
		t.Fatalf("LaunchOrigin = %q, want %q", got, "actor-token")
	}
}

func TestLaunchOriginRoundTripsThroughApplyAndGet(t *testing.T) {
	want := ConvoyFields{
		Owner:        "owner-token",
		Notify:       "notify-token",
		Molecule:     "mol-1",
		Merge:        "direct",
		Target:       "main",
		LaunchOrigin: "actor-token",
	}
	b := beads.Bead{}
	ApplyConvoyFields(&b, want)

	if got := GetConvoyFields(b); got != want {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

// SetConvoyFields is the post-creation route. It must reach the same key the
// create-time route writes, otherwise an origin added later would be invisible
// to readers.
func TestSetConvoyFieldsWritesLaunchOrigin(t *testing.T) {
	store := beads.NewMemStore()
	created, err := store.Create(beads.Bead{Title: "convoy", Type: "convoy"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := SetConvoyFields(store, created.ID, ConvoyFields{LaunchOrigin: "actor-token"}); err != nil {
		t.Fatalf("SetConvoyFields: %v", err)
	}

	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if origin := GetConvoyFields(got).LaunchOrigin; origin != "actor-token" {
		t.Fatalf("persisted LaunchOrigin = %q, want %q", origin, "actor-token")
	}
}

// Every ConvoyFields member must be carried by the key table; a field added to
// the struct but not the table would silently never persist.
func TestEveryConvoyFieldIsCarriedByTheKeyTable(t *testing.T) {
	structFields := reflect.TypeOf(ConvoyFields{}).NumField()
	if structFields != len(convoyFieldKeys) {
		t.Fatalf("ConvoyFields has %d fields but the key table has %d entries; add the missing key",
			structFields, len(convoyFieldKeys))
	}
}
