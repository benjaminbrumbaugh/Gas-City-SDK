package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/mail/beadmail"
)

// Mail addressed to a session bead has no owner once that session retires.
//
// The WORK sweeps next door deliberately skip it: releasing a mail bead clears
// its assignee, and the assignee IS the delivery route, so a released message
// becomes undeliverable rather than unclaimed (ra-59207). Excluding it stopped
// the destruction but left the message open forever, assigned to a closed
// session — `gc doctor session-model` then reports closed-bead-owner with no
// remedy, and nothing in the system ever clears it (gc-p0hf4). Two real
// peer-to-peer messages sat that way for days, each carrying a finding its
// sender expected to be acted on.
//
// Mail needs the opposite move from work: re-ADDRESS it rather than unassign
// it. The retiring session's fallback route (its template, else its agent name)
// is a mailbox the successor session or pool reads, so a message re-pointed
// there is delivered instead of stranded. With no route to move it to, an
// explicit close naming the retired session beats an open bead nobody can act
// on: the content survives on the closed bead and the permanent doctor finding
// clears.

// mailRehomeMetadataFrom records the session a message was rehomed away from.
// The keys are declared in internal/beadmeta/keys.go rather than spelled as
// raw literals here, which is what TestNoUndeclaredMetadataKeys requires of
// every gc.* bead-metadata key written by non-test Go.
const (
	mailRehomeMetadataFrom  = beadmeta.RehomedFromSessionMetadataKey
	mailRehomeMetadataAt    = beadmeta.RehomedAtMetadataKey
	mailRehomeMetadataRoute = beadmeta.RehomedToMetadataKey
)

// mailStrandedCloseReason is stamped on a message closed because its recipient
// session retired with no successor route. It names the session so the record
// stays actionable after the fact.
func mailStrandedCloseReason(retiredSessionID string) string {
	return fmt.Sprintf("recipient session %s retired with no successor route; message preserved unread for review", retiredSessionID)
}

// mailRehomeResult reports one rehome sweep. Rehomed counts messages
// re-addressed to a live route, Expired counts messages closed because no route
// existed, and Failed counts legs that could not be read plus writes that
// errored — a caller that reports a clean retirement while Failed > 0 would be
// claiming mail was handled when it may still be stranded somewhere.
type mailRehomeResult struct {
	Rehomed int
	Expired int
	Failed  int
}

// rehomeMailAssignedToRetiredSession re-addresses (or explicitly closes) every
// open message bead still addressed to a retiring session, across the same leg
// set the work sweep walks.
//
// Reusing that leg set is correct for today's storage shapes: messaging and
// sessions are distinct coordination classes, but the only servable split shape
// (storageSplitWhole) puts them in the same binding, which is exactly why the
// work sweep's own legs already surface mail beads — excludeMailMessageBeads
// exists precisely because they turned up there.
func rehomeMailAssignedToRetiredSession(
	cityPath string,
	cfg *config.City,
	store beads.Store,
	rigStores map[string]beads.Store,
	identifiers []string,
	retiredSessionID string,
	fallbackRoute string,
	now time.Time,
	stderr io.Writer,
) mailRehomeResult {
	var res mailRehomeResult
	if store == nil || strings.TrimSpace(retiredSessionID) == "" || len(identifiers) == 0 {
		return res
	}
	if stderr == nil {
		stderr = io.Discard
	}
	route := strings.TrimSpace(fallbackRoute)
	seen := make(map[string]struct{})
	complete := sweepAssignedWorkLegs(cityPath, cfg, store, rigStores, identifiers, stderr, func(storeIndex int, ownerStore beads.Store) {
		if ownerStore == nil {
			return
		}
		mailStore := beads.MailStore{Store: ownerStore}
		// Mail has no in_progress state — a message is open until it is
		// archived — so unlike the work sweep this walks "open" only.
		for _, assignee := range identifiers {
			items, err := mailStore.List(beads.ListQuery{Assignee: assignee, Status: "open"})
			if err != nil {
				fmt.Fprintf(stderr, "session beads: listing mail addressed to retired session %s via %q: %v\n", retiredSessionID, assignee, err) //nolint:errcheck
				res.Failed++
				continue
			}
			for _, item := range items {
				if !beadmail.IsMessageBead(item) {
					continue
				}
				key := strconv.Itoa(storeIndex) + "\x00" + item.ID
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				if err := rehomeOneMailBead(mailStore, item, retiredSessionID, route, now); err != nil {
					fmt.Fprintf(stderr, "session beads: rehoming mail %s addressed to retired session %s: %v\n", item.ID, retiredSessionID, err) //nolint:errcheck
					res.Failed++
					continue
				}
				if route == "" {
					res.Expired++
					continue
				}
				res.Rehomed++
			}
		}
	})
	if !complete {
		// A partial leg walk means some ledger was never read. Say so through
		// Failed rather than letting the caller read "0 stranded" off a sweep
		// that did not look everywhere.
		res.Failed++
	}
	return res
}

// rehomeOneMailBead re-addresses one message to route, or closes it with a
// reason naming the retired session when there is no route.
//
// The provenance stamp goes on in both cases and BEFORE the close, so a message
// that ends up closed still records where it came from: a close is the one
// outcome nobody can undo by reading the assignee.
func rehomeOneMailBead(store beads.MailStore, item beads.Bead, retiredSessionID, route string, now time.Time) error {
	if store.Store == nil || strings.TrimSpace(item.ID) == "" {
		return nil
	}
	if err := store.SetMetadata(item.ID, mailRehomeMetadataFrom, retiredSessionID); err != nil {
		return err
	}
	if err := store.SetMetadata(item.ID, mailRehomeMetadataAt, now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if route == "" {
		// close_reason is metadata, stamped before Close so BdStore can
		// forward it as `bd close --reason` under validation.on-close=error —
		// the same order beadmail's own retention sweep uses.
		if err := store.SetMetadata(item.ID, "close_reason", mailStrandedCloseReason(retiredSessionID)); err != nil {
			return err
		}
		return store.Close(item.ID)
	}
	if err := store.SetMetadata(item.ID, mailRehomeMetadataRoute, route); err != nil {
		return err
	}
	return store.Update(item.ID, beads.UpdateOpts{Assignee: &route})
}

// firstNonEmptyRoute returns the first non-blank route, so a caller that has no
// WORK run_target to offer still falls back to the mailbox the session bead
// itself names.
func firstNonEmptyRoute(routes ...string) string {
	for _, route := range routes {
		if r := strings.TrimSpace(route); r != "" {
			return r
		}
	}
	return ""
}
