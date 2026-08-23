package routingdecision

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	bbolt "go.etcd.io/bbolt"
)

const (
	// StoreRelativePath is the only controller routing-decision ledger path.
	StoreRelativePath  = ".gc/routing-decisions.db"
	defaultLockTimeout = 250 * time.Millisecond
	maxActiveQuery     = 256
	maxOutcomeQuery    = 100
	retentionMonths    = 6
)

var (
	// ErrStoreLocked classifies bounded failure to acquire the ledger lock.
	ErrStoreLocked = errors.New("routing decision store is locked")
	// ErrUnsupportedSchema classifies a ledger created by a newer implementation.
	ErrUnsupportedSchema = errors.New("unsupported routing decision store schema")
	// ErrStoreCorrupt classifies structural or serialized ledger corruption.
	ErrStoreCorrupt = errors.New("routing decision store is corrupt")
	// ErrDecisionNotFound classifies a missing decision ID.
	ErrDecisionNotFound = errors.New("routing decision not found")
	// ErrDecisionExists classifies a conflicting create for an existing ID.
	ErrDecisionExists = errors.New("routing decision already exists")
	// ErrStaleRevision classifies a lifecycle compare-and-swap failure.
	ErrStaleRevision = errors.New("stale routing decision revision")
	// ErrInvalidTransition classifies an edge outside the fixed state graph.
	ErrInvalidTransition = errors.New("invalid routing decision transition")
	// ErrIdempotencyConflict classifies token reuse for a different request.
	ErrIdempotencyConflict = errors.New("routing decision idempotency conflict")
	// ErrAdmissionCallback classifies a failed external bead mutation without
	// retaining or exposing the collaborator's raw error.
	ErrAdmissionCallback = errors.New("routing decision admission callback failed")
	// ErrAdmissionCommit classifies a lifecycle commit failure after the
	// admission callback without exposing storage details.
	ErrAdmissionCommit = errors.New("routing decision admission commit failed")
)

var (
	bucketMeta              = []byte("meta")
	bucketDecisions         = []byte("decisions")
	bucketStateExpiry       = []byte("state_expiry")
	bucketStateCounts       = []byte("state_counts")
	bucketIdempotency       = []byte("idempotency")
	bucketReceiptByDecision = []byte("receipt_by_decision")
	bucketTransitions       = []byte("transitions")
	bucketImports           = []byte("imports")
	bucketPurgedDecisions   = []byte("purged_decisions")
	keySchemaVersion        = []byte("schema_version")
	keyStoreRevision        = []byte("store_revision")
	keyReceiptIndexFloor    = []byte("receipt_index_floor_revision")
	requiredBucketNames     = [][]byte{
		bucketMeta, bucketDecisions, bucketStateExpiry, bucketStateCounts, bucketIdempotency, bucketReceiptByDecision,
		bucketTransitions, bucketImports, bucketPurgedDecisions,
	}
)

type boltTx = bbolt.Tx

// State is a durable routing-decision lifecycle state.
type State string

const (
	StateProposed         State = "proposed"
	StateApproved         State = "approved"
	StateAdmitted         State = "admitted"
	StateRefusedAfterRace State = "refused_after_race"
	StateExpired          State = "expired"
	StateRevoked          State = "revoked"
	StateClaimed          State = "claimed"
	StateOutcomeRecorded  State = "outcome_recorded"
)

// AllStates returns every schema state in stable order.
func AllStates() []State {
	return []State{StateProposed, StateApproved, StateAdmitted, StateRefusedAfterRace, StateExpired, StateRevoked, StateClaimed, StateOutcomeRecorded}
}

// IsAllowedTransition reports whether from-to is an exact schema edge.
func IsAllowedTransition(from, to State) bool {
	switch from {
	case StateProposed:
		return to == StateApproved || to == StateRevoked || to == StateExpired
	case StateApproved:
		return to == StateAdmitted || to == StateRefusedAfterRace || to == StateRevoked || to == StateExpired
	case StateAdmitted:
		return to == StateClaimed || to == StateOutcomeRecorded
	case StateClaimed:
		return to == StateOutcomeRecorded
	default:
		return false
	}
}

// IsActiveState reports whether a decision still participates in admission or
// lifecycle reconciliation and is therefore categorically retention-immune.
func IsActiveState(state State) bool {
	return state == StateProposed || state == StateApproved || state == StateAdmitted || state == StateClaimed
}

// IsTerminalState reports whether a decision has completed its lifecycle and
// may become eligible for retention purge.
func IsTerminalState(state State) bool {
	return state == StateRefusedAfterRace || state == StateExpired || state == StateRevoked || state == StateOutcomeRecorded
}

// TransitionAudit records one immutable lifecycle change.
type TransitionAudit struct {
	DecisionID     string    `json:"decision_id"`
	From           State     `json:"from"`
	To             State     `json:"to"`
	At             time.Time `json:"at"`
	Reason         string    `json:"reason"`
	RecordRevision uint64    `json:"record_revision"`
	StoreRevision  uint64    `json:"store_revision"`
}

// Record is the full durable decision and its current lifecycle metadata.
type Record struct {
	Payload        DecisionPayload  `json:"payload"`
	Approval       *ApprovalPayload `json:"approval,omitempty"`
	Signature      *Signature       `json:"signature,omitempty"`
	State          State            `json:"state"`
	RecordRevision uint64           `json:"record_revision"`
	StoreRevision  uint64           `json:"store_revision"`
}

// TransitionRequest is a payload-immutable lifecycle compare-and-swap.
type TransitionRequest struct {
	DecisionID       string           `json:"decision_id"`
	ExpectedRevision uint64           `json:"expected_revision"`
	From             State            `json:"from"`
	To               State            `json:"to"`
	Approval         *ApprovalPayload `json:"approval,omitempty"`
	Signature        *Signature       `json:"signature,omitempty"`
	IdempotencyToken string           `json:"idempotency_token"`
	Reason           string           `json:"reason"`
}

// TransitionReceipt is the immutable result retained for idempotent replay.
type TransitionReceipt struct {
	DecisionID     string `json:"decision_id"`
	State          State  `json:"state"`
	RecordRevision uint64 `json:"record_revision"`
	StoreRevision  uint64 `json:"store_revision"`
}

// IngestApprovedRequest is one externally signed, currently valid decision
// envelope plus its request-bound idempotency token.
type IngestApprovedRequest struct {
	Payload          DecisionPayload `json:"payload"`
	Approval         ApprovalPayload `json:"approval"`
	Signature        Signature       `json:"signature"`
	Now              time.Time       `json:"-"`
	IdempotencyToken string          `json:"-"`
}

// IngestApprovedResult contains the immutable approved record and receipt
// produced by atomic ingest.
type IngestApprovedResult struct {
	Record  Record            `json:"record"`
	Receipt TransitionReceipt `json:"receipt"`
}

// DecisionWithAudits is one full decision plus its ordered transition history.
type DecisionWithAudits struct {
	Record Record            `json:"record"`
	Audits []TransitionAudit `json:"audits"`
	// AdmissionReceiptID is the durable idempotency-receipt identity for the
	// authoritative admitted edge. It is internal projection evidence and must
	// not alter the existing decision-list wire shape.
	AdmissionReceiptID string `json:"-"`
}

// ListOptions controls one bounded decision-ID keyset page.
type ListOptions struct {
	State  State
	Limit  int
	Cursor string
}

// DecisionPage is one snapshot-consistent keyset page.
type DecisionPage struct {
	Items      []DecisionWithAudits `json:"items"`
	Total      int                  `json:"total"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// StateCount is one exact lifecycle-state cardinality.
type StateCount struct {
	State State  `json:"state"`
	Count uint64 `json:"count"`
}

// StoreStatus is the exact current ledger summary used by the live status API.
type StoreStatus struct {
	SchemaVersion uint64       `json:"schema_version"`
	StoreRevision uint64       `json:"store_revision"`
	StateCounts   []StateCount `json:"state_counts"`
}

// PurgeOptions controls one bounded terminal-retention page.
type PurgeOptions struct {
	Now    time.Time
	Limit  int
	Cursor string
}

// PurgeResult reports bounded scan and deletion progress.
type PurgeResult struct {
	Scanned    int    `json:"scanned"`
	Deleted    int    `json:"deleted"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// FinalAdmissionRequest identifies the approved CAS coordinated with one bead write.
type FinalAdmissionRequest struct {
	DecisionID       string    `json:"decision_id"`
	ExpectedRevision uint64    `json:"expected_revision"`
	Now              time.Time `json:"now"`
	IdempotencyToken string    `json:"idempotency_token"`
}

// AdmissionCallbackResult chooses the only two callback-owned lifecycle outcomes.
type AdmissionCallbackResult struct {
	State  State
	Reason string
}

// StoreOptions controls local opening and deterministic tests.
type StoreOptions struct {
	Timeout time.Duration
	Now     func() time.Time
	// BeforeAdmissionCommit is a deterministic fault-injection seam. Production
	// callers leave it nil; tests use it to prove cross-store compensation.
	BeforeAdmissionCommit func() error
}

// Store owns one process-lifetime bbolt handle to a city's decision ledger.
type Store struct {
	db                    *bbolt.DB
	path                  string
	now                   func() time.Time
	beforeAdmissionCommit func() error
}

type idempotencyRecord struct {
	Kind        string             `json:"kind"`
	Fingerprint string             `json:"fingerprint"`
	DecisionID  string             `json:"decision_id,omitempty"`
	Created     *Record            `json:"created,omitempty"`
	Transition  *TransitionReceipt `json:"transition,omitempty"`
}

// IngestApproved atomically validates and persists one currently valid signed
// decision. It records the proposed and approved lifecycle edges in the same
// writer transaction, so no unsigned intermediate state is externally visible.
func (store *Store) IngestApproved(request IngestApprovedRequest, verifier Verifier) (IngestApprovedResult, error) {
	useStoreClock := request.Now.IsZero()
	if !useStoreClock {
		request.Now = request.Now.UTC()
	}
	if err := validateText("idempotency token", request.IdempotencyToken, true); err != nil {
		return IngestApprovedResult{}, err
	}
	if err := request.Payload.Validate(); err != nil {
		return IngestApprovedResult{}, err
	}
	if err := request.Approval.Validate(); err != nil {
		return IngestApprovedResult{}, err
	}
	approvedAt := request.Approval.ApprovedAt.UTC()
	if approvedAt.Before(request.Payload.CreatedAt.UTC()) || !approvedAt.Before(request.Payload.ExpiresAt.UTC()) {
		return IngestApprovedResult{}, invalidf("approval time is outside the current decision window")
	}
	if err := verifier.Verify(request.Payload, request.Approval, request.Signature); err != nil {
		return IngestApprovedResult{}, err
	}
	fingerprint, err := ingestFingerprint(request.Payload, request.Approval, request.Signature)
	if err != nil {
		return IngestApprovedResult{}, err
	}

	payload := canonicalPayloadCopy(request.Payload)
	approval := canonicalApprovalCopy(request.Approval)
	signature := *cloneSignature(&request.Signature)
	var result IngestApprovedResult
	err = store.db.Update(func(tx *bbolt.Tx) error {
		transactionNow := request.Now
		if useStoreClock {
			transactionNow = store.now().UTC()
		}
		if !payload.IsActiveAt(transactionNow) || approvedAt.After(transactionNow) {
			return invalidf("decision or approval is not current")
		}
		if replay, found, err := readIdempotency(tx, request.IdempotencyToken, fingerprint); err != nil {
			return err
		} else if found {
			if replay.Kind != "ingest" || replay.Transition == nil || replay.DecisionID != payload.DecisionID {
				return ErrIdempotencyConflict
			}
			result = ingestResultFromReceipt(payload, approval, signature, *replay.Transition)
			return nil
		}
		if tx.Bucket(bucketPurgedDecisions).Get([]byte(payload.DecisionID)) != nil || tx.Bucket(bucketDecisions).Get([]byte(payload.DecisionID)) != nil {
			return ErrDecisionExists
		}

		proposedStoreRevision, err := nextStoreRevision(tx)
		if err != nil {
			return err
		}
		proposedAudit := TransitionAudit{
			DecisionID: payload.DecisionID, To: StateProposed, At: transactionNow,
			Reason: "ingested signed decision", RecordRevision: 1, StoreRevision: proposedStoreRevision,
		}
		if err := putAudit(tx, proposedAudit); err != nil {
			return err
		}

		approvedStoreRevision, err := nextStoreRevision(tx)
		if err != nil {
			return err
		}
		record := Record{
			Payload: payload, Approval: &approval, Signature: &signature, State: StateApproved,
			RecordRevision: 2, StoreRevision: approvedStoreRevision,
		}
		if err := putRecord(tx, record); err != nil {
			return err
		}
		if err := adjustStateCount(tx, StateApproved, 1); err != nil {
			return err
		}
		approvedAudit := TransitionAudit{
			DecisionID: payload.DecisionID, From: StateProposed, To: StateApproved, At: transactionNow,
			Reason: "signature admitted", RecordRevision: 2, StoreRevision: approvedStoreRevision,
		}
		if err := putAudit(tx, approvedAudit); err != nil {
			return err
		}
		receipt := TransitionReceipt{
			DecisionID: payload.DecisionID, State: StateApproved,
			RecordRevision: record.RecordRevision, StoreRevision: record.StoreRevision,
		}
		storedReceipt := idempotencyRecord{
			Kind: "ingest", Fingerprint: fingerprint, DecisionID: payload.DecisionID, Transition: &receipt,
		}
		if err := putIdempotency(tx, request.IdempotencyToken, storedReceipt); err != nil {
			return err
		}
		result = IngestApprovedResult{Record: record, Receipt: receipt}
		return nil
	})
	return result, classifyStoreError(err)
}

// ListDecisions returns one snapshot-consistent page in decision-ID order.
// Each call examines at most maxActiveQuery rows even when a state filter is
// sparse; the returned cursor advances by the last scanned ID.
func (store *Store) ListDecisions(options ListOptions) (DecisionPage, error) {
	if options.Limit <= 0 || options.Limit > maxActiveQuery {
		return DecisionPage{}, invalidf("decision list limit must be between 1 and %d", maxActiveQuery)
	}
	if options.State != "" && !knownState(options.State) {
		return DecisionPage{}, invalidf("unknown decision state")
	}
	after, err := decodeKeysetCursor(options.Cursor)
	if err != nil {
		return DecisionPage{}, err
	}
	page := DecisionPage{Items: make([]DecisionWithAudits, 0, options.Limit)}
	err = store.db.View(func(tx *bbolt.Tx) error {
		cursor := tx.Bucket(bucketDecisions).Cursor()
		key, value := cursor.First()
		if after != "" {
			key, value = cursor.Seek([]byte(after))
			if key != nil && string(key) == after {
				key, value = cursor.Next()
			}
		}
		scanned := 0
		lastScanned := ""
		for key != nil && scanned < maxActiveQuery && len(page.Items) < options.Limit {
			var record Record
			if err := decodeRecord(value, &record); err != nil || string(key) != record.Payload.DecisionID {
				return ErrStoreCorrupt
			}
			scanned++
			lastScanned = string(key)
			if options.State == "" || record.State == options.State {
				audits, err := auditsForDecision(tx, record.Payload.DecisionID)
				if err != nil {
					return err
				}
				page.Items = append(page.Items, DecisionWithAudits{Record: record, Audits: audits})
			}
			key, value = cursor.Next()
		}
		if key != nil && lastScanned != "" {
			page.NextCursor = encodeKeysetCursor(lastScanned)
		}
		return nil
	})
	page.Total = len(page.Items)
	return page, classifyStoreError(err)
}

// ListOutcomeDecisions returns claimed and terminal recommendation-outcome
// candidates in stable decision-ID order. Each request returns at most 100
// items and examines at most 100 ledger rows; a sparse/truncated scan advances
// by the last examined decision ID so repeated calls make bounded progress.
func (store *Store) ListOutcomeDecisions(options OutcomeListOptions) (DecisionPage, error) {
	if options.Limit <= 0 || options.Limit > maxOutcomeQuery {
		return DecisionPage{}, invalidf("outcome list limit must be between 1 and %d", maxOutcomeQuery)
	}
	after, err := decodeKeysetCursor(options.Cursor)
	if err != nil {
		return DecisionPage{}, err
	}
	page := DecisionPage{Items: make([]DecisionWithAudits, 0, options.Limit)}
	err = store.db.View(func(tx *bbolt.Tx) error {
		cursor := tx.Bucket(bucketDecisions).Cursor()
		key, value := cursor.First()
		if after != "" {
			key, value = cursor.Seek([]byte(after))
			if key != nil && string(key) == after {
				key, value = cursor.Next()
			}
		}
		scanned := 0
		lastScanned := ""
		for key != nil && scanned < maxOutcomeQuery && len(page.Items) < options.Limit {
			var record Record
			if err := decodeRecord(value, &record); err != nil || string(key) != record.Payload.DecisionID {
				return ErrStoreCorrupt
			}
			scanned++
			lastScanned = string(key)
			validRecommendation := validateText("recommendation_id", record.Payload.RecommendationID, true) == nil
			if validRecommendation && (record.State == StateClaimed || IsTerminalState(record.State)) {
				audits, err := auditsForDecision(tx, record.Payload.DecisionID)
				if err != nil {
					return err
				}
				admissionReceiptID, err := admissionReceiptIDForDecision(tx, record.Payload.DecisionID)
				if err != nil {
					return err
				}
				page.Items = append(page.Items, DecisionWithAudits{Record: record, Audits: audits, AdmissionReceiptID: admissionReceiptID})
			}
			key, value = cursor.Next()
		}
		if key != nil && lastScanned != "" {
			page.NextCursor = encodeKeysetCursor(lastScanned)
		}
		return nil
	})
	page.Total = len(page.Items)
	return page, classifyStoreError(err)
}

func admissionReceiptIDForDecision(tx *bbolt.Tx, decisionID string) (string, error) {
	prefix := append(append([]byte(nil), []byte(decisionID)...), 0)
	cursor := tx.Bucket(bucketReceiptByDecision).Cursor()
	result := ""
	for key, indexedToken := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, indexedToken = cursor.Next() {
		raw := tx.Bucket(bucketIdempotency).Get(indexedToken)
		if raw == nil {
			return "", ErrStoreCorrupt
		}
		var receipt idempotencyRecord
		if strictUnmarshal(raw, &receipt) != nil || receipt.DecisionID != decisionID {
			return "", ErrStoreCorrupt
		}
		if receipt.Kind != "admission" || receipt.Transition == nil || receipt.Transition.State != StateAdmitted {
			continue
		}
		candidate := string(indexedToken)
		if result != "" && result != candidate {
			return "", ErrStoreCorrupt
		}
		result = candidate
	}
	return result, nil
}

// Status returns exact counts from the transactionally maintained state index.
func (store *Store) Status() (StoreStatus, error) {
	status := StoreStatus{StateCounts: make([]StateCount, 0, len(AllStates()))}
	err := store.db.View(func(tx *bbolt.Tx) error {
		var ok bool
		status.SchemaVersion, ok = decodeUint64(tx.Bucket(bucketMeta).Get(keySchemaVersion))
		if !ok || status.SchemaVersion != SchemaVersion {
			return ErrStoreCorrupt
		}
		status.StoreRevision, ok = decodeUint64(tx.Bucket(bucketMeta).Get(keyStoreRevision))
		if !ok {
			return ErrStoreCorrupt
		}
		for _, state := range AllStates() {
			count, ok := decodeUint64(tx.Bucket(bucketStateCounts).Get([]byte(state)))
			if !ok {
				return ErrStoreCorrupt
			}
			status.StateCounts = append(status.StateCounts, StateCount{State: state, Count: count})
		}
		return nil
	})
	return status, classifyStoreError(err)
}

// PurgeTerminal scans one bounded decision-ID page and atomically deletes
// terminal records whose latest transition is at least six calendar months
// old, together with their state index, audits, and indexed receipts.
func (store *Store) PurgeTerminal(options PurgeOptions) (PurgeResult, error) {
	if options.Limit <= 0 || options.Limit > maxActiveQuery {
		return PurgeResult{}, invalidf("purge limit must be between 1 and %d", maxActiveQuery)
	}
	if options.Now.IsZero() {
		options.Now = store.now().UTC()
	} else {
		options.Now = options.Now.UTC()
	}
	after, err := decodeKeysetCursor(options.Cursor)
	if err != nil {
		return PurgeResult{}, err
	}
	cutoff := subtractCalendarMonths(options.Now, retentionMonths)
	result := PurgeResult{}
	err = store.db.Update(func(tx *bbolt.Tx) error {
		type candidate struct {
			record Record
		}
		candidates := make([]candidate, 0, options.Limit)
		cursor := tx.Bucket(bucketDecisions).Cursor()
		key, value := cursor.First()
		if after != "" {
			key, value = cursor.Seek([]byte(after))
			if key != nil && string(key) == after {
				key, value = cursor.Next()
			}
		}
		lastScanned := ""
		for key != nil && result.Scanned < options.Limit {
			var record Record
			if err := decodeRecord(value, &record); err != nil || string(key) != record.Payload.DecisionID {
				return ErrStoreCorrupt
			}
			result.Scanned++
			lastScanned = string(key)
			if IsTerminalState(record.State) {
				audit, err := latestAudit(tx, record)
				if err != nil {
					return err
				}
				if !audit.At.After(cutoff) {
					candidates = append(candidates, candidate{record: record})
				}
			}
			key, value = cursor.Next()
		}
		if key != nil && lastScanned != "" {
			result.NextCursor = encodeKeysetCursor(lastScanned)
		}
		for _, candidate := range candidates {
			if err := purgeDecisionTx(tx, candidate.record); err != nil {
				return err
			}
			result.Deleted++
		}
		return nil
	})
	return result, classifyStoreError(err)
}

// FinalAdmission holds the bbolt writer transaction while rechecking the exact
// approved record and signature, running one bounded bead callback, and
// committing admitted/refused lifecycle state. The callback must not reenter
// this store or perform provider/network work.
func (store *Store) FinalAdmission(request FinalAdmissionRequest, verifier Verifier, callback func(Record) (AdmissionCallbackResult, error)) (TransitionReceipt, error) {
	useStoreClock := request.Now.IsZero()
	if !useStoreClock {
		request.Now = request.Now.UTC()
	}
	var receipt TransitionReceipt
	err := store.db.Update(func(tx *bbolt.Tx) error {
		if strings.TrimSpace(request.DecisionID) == "" || strings.TrimSpace(request.IdempotencyToken) == "" || callback == nil {
			return fmt.Errorf("%w: incomplete final admission", ErrInvalidDecision)
		}
		fingerprint := fingerprintOf(struct {
			Kind             string `json:"kind"`
			DecisionID       string `json:"decision_id"`
			ExpectedRevision uint64 `json:"expected_revision"`
		}{Kind: "admission", DecisionID: request.DecisionID, ExpectedRevision: request.ExpectedRevision})
		if replay, found, err := readIdempotency(tx, request.IdempotencyToken, fingerprint); err != nil {
			return err
		} else if found {
			if replay.Kind != "admission" || replay.Transition == nil {
				return ErrIdempotencyConflict
			}
			receipt = *replay.Transition
			return nil
		}
		var record Record
		value := tx.Bucket(bucketDecisions).Get([]byte(request.DecisionID))
		if value == nil {
			return ErrDecisionNotFound
		}
		if err := decodeRecord(value, &record); err != nil {
			return err
		}
		if record.RecordRevision != request.ExpectedRevision {
			return ErrStaleRevision
		}
		if record.State != StateApproved || record.Approval == nil || record.Signature == nil {
			return ErrInvalidTransition
		}
		if err := verifier.Verify(record.Payload, *record.Approval, *record.Signature); err != nil {
			return err
		}
		transactionNow := request.Now
		if useStoreClock {
			transactionNow = store.now().UTC()
		}
		result := AdmissionCallbackResult{}
		if !transactionNow.Before(record.Payload.ExpiresAt.UTC()) {
			result = AdmissionCallbackResult{State: StateExpired, Reason: "validity window elapsed"}
		} else {
			if transactionNow.Before(record.Payload.CreatedAt.UTC()) {
				return ErrInvalidTransition
			}
			var callbackErr error
			result, callbackErr = callback(record)
			if callbackErr != nil {
				return ErrAdmissionCallback
			}
			if result.State != StateAdmitted && result.State != StateRefusedAfterRace {
				return fmt.Errorf("%w: callback state", ErrInvalidTransition)
			}
			if err := validateText("admission reason", result.Reason, true); err != nil {
				return err
			}
		}
		if store.beforeAdmissionCommit != nil && store.beforeAdmissionCommit() != nil {
			return ErrAdmissionCommit
		}
		oldIndex := stateIndexKey(record)
		storeRevision, err := nextStoreRevision(tx)
		if err != nil {
			return err
		}
		record.State = result.State
		record.RecordRevision++
		record.StoreRevision = storeRevision
		if err := tx.Bucket(bucketStateExpiry).Delete(oldIndex); err != nil {
			return ErrStoreCorrupt
		}
		if err := putRecord(tx, record); err != nil {
			return err
		}
		if err := moveStateCount(tx, StateApproved, result.State); err != nil {
			return err
		}
		audit := TransitionAudit{DecisionID: record.Payload.DecisionID, From: StateApproved, To: result.State, At: transactionNow, Reason: result.Reason, RecordRevision: record.RecordRevision, StoreRevision: storeRevision}
		if err := putAudit(tx, audit); err != nil {
			return err
		}
		receipt = TransitionReceipt{DecisionID: record.Payload.DecisionID, State: record.State, RecordRevision: record.RecordRevision, StoreRevision: storeRevision}
		idempotency := idempotencyRecord{Kind: "admission", Fingerprint: fingerprint, DecisionID: record.Payload.DecisionID, Transition: &receipt}
		return putIdempotency(tx, request.IdempotencyToken, idempotency)
	})
	return receipt, classifyStoreError(err)
}

// OpenStore opens or creates the fixed 0600 ledger with a bounded lock wait.
func OpenStore(cityRoot string, options StoreOptions) (*Store, error) {
	if options.Timeout <= 0 {
		options.Timeout = defaultLockTimeout
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	path, existed, err := prepareStorePath(cityRoot)
	if err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{
		Timeout: options.Timeout,
		OpenFile: func(name string, flag int, mode os.FileMode) (*os.File, error) {
			if filepath.Clean(name) != filepath.Clean(path) {
				return nil, errors.New("unexpected database path")
			}
			return openBoltFile(name, flag, mode)
		},
	})
	if err != nil {
		if errors.Is(err, bbolt.ErrTimeout) {
			return nil, ErrStoreLocked
		}
		return nil, fmt.Errorf("%w: open ledger", ErrStoreCorrupt)
	}
	store := &Store{db: db, path: path, now: options.Now, beforeAdmissionCommit: options.BeforeAdmissionCommit}
	if err := store.initialize(); err != nil {
		_ = db.Close()
		if !existed {
			removeExactNewStore(path) //nolint:errcheck // only best-effort cleanup of this creation
		}
		return nil, err
	}
	return store, nil
}

func (store *Store) initialize() error {
	return store.db.Update(func(tx *bbolt.Tx) error {
		meta := tx.Bucket(bucketMeta)
		if meta != nil && meta.Get(keySchemaVersion) != nil {
			schema, ok := decodeUint64(meta.Get(keySchemaVersion))
			if !ok {
				return ErrStoreCorrupt
			}
			if schema != SchemaVersion {
				return ErrUnsupportedSchema
			}
		}
		hadReceiptIndex := tx.Bucket(bucketReceiptByDecision) != nil
		hadStateCounts := tx.Bucket(bucketStateCounts) != nil
		for _, name := range requiredBucketNames {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("%w: initialize bucket", ErrStoreCorrupt)
			}
		}
		meta = tx.Bucket(bucketMeta)
		rawSchema := meta.Get(keySchemaVersion)
		if rawSchema == nil {
			if err := meta.Put(keySchemaVersion, encodeUint64(SchemaVersion)); err != nil {
				return err
			}
			if err := meta.Put(keyStoreRevision, encodeUint64(0)); err != nil {
				return err
			}
			if err := meta.Put(keyReceiptIndexFloor, encodeUint64(0)); err != nil {
				return err
			}
			for _, state := range AllStates() {
				if err := tx.Bucket(bucketStateCounts).Put([]byte(state), encodeUint64(0)); err != nil {
					return ErrStoreCorrupt
				}
			}
			return nil
		}
		schema, ok := decodeUint64(rawSchema)
		if !ok {
			return ErrStoreCorrupt
		}
		if schema != SchemaVersion {
			return ErrUnsupportedSchema
		}
		if _, ok := decodeUint64(meta.Get(keyStoreRevision)); !ok {
			return ErrStoreCorrupt
		}
		if !hadReceiptIndex {
			floor, _ := decodeUint64(meta.Get(keyStoreRevision))
			if err := meta.Put(keyReceiptIndexFloor, encodeUint64(floor)); err != nil {
				return ErrStoreCorrupt
			}
		} else if _, ok := decodeUint64(meta.Get(keyReceiptIndexFloor)); !ok {
			return ErrStoreCorrupt
		}
		if !hadStateCounts {
			if err := rebuildStateCounts(tx); err != nil {
				return err
			}
		} else {
			for _, state := range AllStates() {
				if _, ok := decodeUint64(tx.Bucket(bucketStateCounts).Get([]byte(state))); !ok {
					return ErrStoreCorrupt
				}
			}
		}
		return nil
	})
}

// Close releases the process-lifetime ledger lock.
func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

// Create persists one proposed decision and a request-bound idempotency receipt.
func (store *Store) Create(payload DecisionPayload, token string) (Record, error) {
	if err := payload.Validate(); err != nil {
		return Record{}, err
	}
	if strings.TrimSpace(token) == "" {
		return Record{}, fmt.Errorf("%w: empty idempotency token", ErrInvalidDecision)
	}
	fingerprint := fingerprintOf(struct {
		Kind    string            `json:"kind"`
		Payload canonicalDecision `json:"payload"`
	}{Kind: "create", Payload: normalizedDecision(payload, true)})
	var result Record
	err := store.db.Update(func(tx *bbolt.Tx) error {
		if replay, found, err := readIdempotency(tx, token, fingerprint); err != nil {
			return err
		} else if found {
			if replay.Kind != "create" || replay.Created == nil {
				return ErrIdempotencyConflict
			}
			result = *replay.Created
			return nil
		}
		decisions := tx.Bucket(bucketDecisions)
		if decisions.Get([]byte(payload.DecisionID)) != nil {
			return ErrDecisionExists
		}
		storeRevision, err := nextStoreRevision(tx)
		if err != nil {
			return err
		}
		result = Record{Payload: payload, State: StateProposed, RecordRevision: 1, StoreRevision: storeRevision}
		if err := putRecord(tx, result); err != nil {
			return err
		}
		if err := adjustStateCount(tx, StateProposed, 1); err != nil {
			return err
		}
		audit := TransitionAudit{DecisionID: payload.DecisionID, To: StateProposed, At: store.now().UTC(), Reason: "created", RecordRevision: 1, StoreRevision: storeRevision}
		if err := putAudit(tx, audit); err != nil {
			return err
		}
		receipt := idempotencyRecord{Kind: "create", Fingerprint: fingerprint, DecisionID: payload.DecisionID, Created: &result}
		return putIdempotency(tx, token, receipt)
	})
	return result, classifyStoreError(err)
}

// Get returns one decision by exact ID.
func (store *Store) Get(decisionID string) (Record, error) {
	var result Record
	err := store.db.View(func(tx *bbolt.Tx) error {
		value := tx.Bucket(bucketDecisions).Get([]byte(decisionID))
		if value == nil {
			return ErrDecisionNotFound
		}
		return decodeRecord(value, &result)
	})
	return result, classifyStoreError(err)
}

// Transition performs one exact lifecycle CAS without accepting payload edits.
func (store *Store) Transition(request TransitionRequest, verifier Verifier) (TransitionReceipt, error) {
	var receipt TransitionReceipt
	err := store.db.Update(func(tx *bbolt.Tx) error {
		var err error
		receipt, err = store.transitionTx(tx, request, verifier)
		return err
	})
	return receipt, classifyStoreError(err)
}

func (store *Store) transitionTx(tx *bbolt.Tx, request TransitionRequest, verifier Verifier) (TransitionReceipt, error) {
	if strings.TrimSpace(request.DecisionID) == "" {
		return TransitionReceipt{}, fmt.Errorf("%w: incomplete transition", ErrInvalidDecision)
	}
	if err := validateText("idempotency token", request.IdempotencyToken, true); err != nil {
		return TransitionReceipt{}, err
	}
	if err := validateText("transition reason", request.Reason, true); err != nil {
		return TransitionReceipt{}, err
	}
	fingerprint := fingerprintOf(request)
	if replay, found, err := readIdempotency(tx, request.IdempotencyToken, fingerprint); err != nil {
		return TransitionReceipt{}, err
	} else if found {
		if replay.Kind != "transition" || replay.Transition == nil {
			return TransitionReceipt{}, ErrIdempotencyConflict
		}
		return *replay.Transition, nil
	}
	var record Record
	value := tx.Bucket(bucketDecisions).Get([]byte(request.DecisionID))
	if value == nil {
		return TransitionReceipt{}, ErrDecisionNotFound
	}
	if err := decodeRecord(value, &record); err != nil {
		return TransitionReceipt{}, err
	}
	if record.RecordRevision != request.ExpectedRevision {
		return TransitionReceipt{}, ErrStaleRevision
	}
	if record.State != request.From || !IsAllowedTransition(request.From, request.To) {
		return TransitionReceipt{}, ErrInvalidTransition
	}
	if request.To == StateApproved {
		if request.Approval == nil || request.Signature == nil {
			return TransitionReceipt{}, ErrAuthorizationRequired
		}
		if err := verifier.Verify(record.Payload, *request.Approval, *request.Signature); err != nil {
			return TransitionReceipt{}, err
		}
		record.Approval = cloneApproval(request.Approval)
		record.Signature = cloneSignature(request.Signature)
	} else if request.Approval != nil || request.Signature != nil {
		return TransitionReceipt{}, fmt.Errorf("%w: approval mutation outside approval edge", ErrInvalidDecision)
	}
	oldIndex := stateIndexKey(record)
	storeRevision, err := nextStoreRevision(tx)
	if err != nil {
		return TransitionReceipt{}, err
	}
	record.State = request.To
	record.RecordRevision++
	record.StoreRevision = storeRevision
	if err := tx.Bucket(bucketStateExpiry).Delete(oldIndex); err != nil {
		return TransitionReceipt{}, err
	}
	if err := putRecord(tx, record); err != nil {
		return TransitionReceipt{}, err
	}
	if err := moveStateCount(tx, request.From, request.To); err != nil {
		return TransitionReceipt{}, err
	}
	audit := TransitionAudit{DecisionID: record.Payload.DecisionID, From: request.From, To: request.To, At: store.now().UTC(), Reason: request.Reason, RecordRevision: record.RecordRevision, StoreRevision: storeRevision}
	if err := putAudit(tx, audit); err != nil {
		return TransitionReceipt{}, err
	}
	receipt := TransitionReceipt{DecisionID: record.Payload.DecisionID, State: record.State, RecordRevision: record.RecordRevision, StoreRevision: storeRevision}
	idempotency := idempotencyRecord{Kind: "transition", Fingerprint: fingerprint, DecisionID: record.Payload.DecisionID, Transition: &receipt}
	if err := putIdempotency(tx, request.IdempotencyToken, idempotency); err != nil {
		return TransitionReceipt{}, err
	}
	return receipt, nil
}

// ActiveApproved returns at most limit authentic records ordered by expiry then ID.
func (store *Store) ActiveApproved(now time.Time, limit int, verifier Verifier) ([]Record, error) {
	if limit <= 0 || limit > maxActiveQuery {
		return nil, fmt.Errorf("%w: active query limit", ErrInvalidDecision)
	}
	result := make([]Record, 0, limit)
	err := store.db.View(func(tx *bbolt.Tx) error {
		cursor := tx.Bucket(bucketStateExpiry).Cursor()
		prefix := stateIndexPrefix(StateApproved)
		for key, _ := cursor.Seek(stateIndexSeek(StateApproved, now)); key != nil && bytes.HasPrefix(key, prefix) && len(result) < limit; key, _ = cursor.Next() {
			var record Record
			decisionID := indexDecisionID(key)
			value := tx.Bucket(bucketDecisions).Get([]byte(decisionID))
			if value == nil || decodeRecord(value, &record) != nil || !bytes.Equal(key, stateIndexKey(record)) {
				return ErrStoreCorrupt
			}
			if !record.Payload.IsActiveAt(now) {
				continue
			}
			if record.Approval == nil || record.Signature == nil || verifier.Verify(record.Payload, *record.Approval, *record.Signature) != nil {
				return ErrInvalidSignature
			}
			result = append(result, record)
		}
		return nil
	})
	return result, classifyStoreError(err)
}

// ExpireDue transitions a bounded set of due proposed/approved records.
func (store *Store) ExpireDue(now time.Time, limit int, tokenFor func(string) string) (int, error) {
	if limit <= 0 || limit > maxActiveQuery || tokenFor == nil {
		return 0, fmt.Errorf("%w: expiry sweep arguments", ErrInvalidDecision)
	}
	type candidate struct {
		id       string
		revision uint64
		state    State
	}
	candidates := make([]candidate, 0, limit)
	err := store.db.View(func(tx *bbolt.Tx) error {
		for _, state := range []State{StateProposed, StateApproved} {
			cursor := tx.Bucket(bucketStateExpiry).Cursor()
			prefix := stateIndexPrefix(state)
			for key, _ := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix) && len(candidates) < limit; key, _ = cursor.Next() {
				var record Record
				value := tx.Bucket(bucketDecisions).Get([]byte(indexDecisionID(key)))
				if value == nil || decodeRecord(value, &record) != nil || !bytes.Equal(key, stateIndexKey(record)) {
					return ErrStoreCorrupt
				}
				if record.Payload.ExpiresAt.After(now) {
					break
				}
				candidates = append(candidates, candidate{id: record.Payload.DecisionID, revision: record.RecordRevision, state: record.State})
			}
		}
		return nil
	})
	if err != nil {
		return 0, classifyStoreError(err)
	}
	expired := 0
	for _, candidate := range candidates {
		_, err := store.Transition(TransitionRequest{DecisionID: candidate.id, ExpectedRevision: candidate.revision, From: candidate.state, To: StateExpired, IdempotencyToken: tokenFor(candidate.id), Reason: "validity window elapsed"}, Verifier{})
		if errors.Is(err, ErrStaleRevision) || errors.Is(err, ErrInvalidTransition) {
			continue
		}
		if err != nil {
			return expired, err
		}
		expired++
	}
	return expired, nil
}

func ingestFingerprint(payload DecisionPayload, approval ApprovalPayload, signature Signature) (string, error) {
	signing, err := SigningBytes(payload, approval)
	if err != nil {
		return "", err
	}
	return fingerprintOf(struct {
		Kind        string `json:"kind"`
		Signing     []byte `json:"signing"`
		Algorithm   string `json:"algorithm"`
		AuthorityID string `json:"authority_id"`
		Signature   []byte `json:"signature"`
	}{
		Kind: "ingest", Signing: signing, Algorithm: signature.Algorithm,
		AuthorityID: signature.AuthorityID, Signature: append([]byte(nil), signature.Value...),
	}), nil
}

func canonicalPayloadCopy(payload DecisionPayload) DecisionPayload {
	payload.Evidence = append([]string{}, payload.Evidence...)
	payload.Alternatives = append([]Alternative{}, payload.Alternatives...)
	payload.Options = append([]AuditOption{}, payload.Options...)
	sort.Slice(payload.Options, func(i, j int) bool { return payload.Options[i].Key < payload.Options[j].Key })
	payload.CreatedAt = payload.CreatedAt.UTC().Round(0)
	payload.ExpiresAt = payload.ExpiresAt.UTC().Round(0)
	return payload
}

func canonicalApprovalCopy(approval ApprovalPayload) ApprovalPayload {
	approval.ApprovedAt = approval.ApprovedAt.UTC().Round(0)
	return approval
}

func ingestResultFromReceipt(payload DecisionPayload, approval ApprovalPayload, signature Signature, receipt TransitionReceipt) IngestApprovedResult {
	record := Record{
		Payload: payload, Approval: &approval, Signature: &signature, State: receipt.State,
		RecordRevision: receipt.RecordRevision, StoreRevision: receipt.StoreRevision,
	}
	return IngestApprovedResult{Record: record, Receipt: receipt}
}

func putIdempotency(tx *bbolt.Tx, token string, receipt idempotencyRecord) error {
	if receipt.DecisionID == "" {
		return ErrStoreCorrupt
	}
	if err := putJSON(tx.Bucket(bucketIdempotency), []byte(token), receipt); err != nil {
		return err
	}
	return tx.Bucket(bucketReceiptByDecision).Put(receiptIndexKey(receipt.DecisionID, token), []byte(token))
}

func receiptIndexKey(decisionID, token string) []byte {
	key := make([]byte, 0, len(decisionID)+len(token)+1)
	key = append(key, decisionID...)
	key = append(key, 0)
	return append(key, token...)
}

func auditsForDecision(tx *bbolt.Tx, decisionID string) ([]TransitionAudit, error) {
	result := make([]TransitionAudit, 0, 4)
	prefix := append([]byte(decisionID), 0)
	cursor := tx.Bucket(bucketTransitions).Cursor()
	for key, value := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, value = cursor.Next() {
		var audit TransitionAudit
		if err := strictUnmarshal(value, &audit); err != nil || !bytes.Equal(key, transitionAuditKey(audit.DecisionID, audit.RecordRevision)) {
			return nil, ErrStoreCorrupt
		}
		result = append(result, audit)
	}
	return result, nil
}

func latestAudit(tx *bbolt.Tx, record Record) (TransitionAudit, error) {
	var audit TransitionAudit
	value := tx.Bucket(bucketTransitions).Get(transitionAuditKey(record.Payload.DecisionID, record.RecordRevision))
	if value == nil || strictUnmarshal(value, &audit) != nil || audit.To != record.State || audit.RecordRevision != record.RecordRevision {
		return TransitionAudit{}, ErrStoreCorrupt
	}
	return audit, nil
}

func purgeDecisionTx(tx *bbolt.Tx, record Record) error {
	decisionID := record.Payload.DecisionID
	if !IsTerminalState(record.State) {
		return ErrInvalidTransition
	}
	if err := tx.Bucket(bucketStateExpiry).Delete(stateIndexKey(record)); err != nil {
		return ErrStoreCorrupt
	}
	if err := tx.Bucket(bucketDecisions).Delete([]byte(decisionID)); err != nil {
		return ErrStoreCorrupt
	}
	transitionPrefix := append([]byte(decisionID), 0)
	transitionCursor := tx.Bucket(bucketTransitions).Cursor()
	for key, _ := transitionCursor.Seek(transitionPrefix); key != nil && bytes.HasPrefix(key, transitionPrefix); key, _ = transitionCursor.Next() {
		if err := transitionCursor.Delete(); err != nil {
			return ErrStoreCorrupt
		}
	}
	receiptPrefix := append([]byte(decisionID), 0)
	receiptCursor := tx.Bucket(bucketReceiptByDecision).Cursor()
	for key, value := receiptCursor.Seek(receiptPrefix); key != nil && bytes.HasPrefix(key, receiptPrefix); key, value = receiptCursor.Next() {
		if err := tx.Bucket(bucketIdempotency).Delete(append([]byte(nil), value...)); err != nil {
			return ErrStoreCorrupt
		}
		if err := receiptCursor.Delete(); err != nil {
			return ErrStoreCorrupt
		}
	}
	if err := tx.Bucket(bucketPurgedDecisions).Put([]byte(decisionID), []byte(fingerprintOf(normalizedDecision(record.Payload, true)))); err != nil {
		return ErrStoreCorrupt
	}
	if err := adjustStateCount(tx, record.State, -1); err != nil {
		return err
	}
	_, err := nextStoreRevision(tx)
	return err
}

func adjustStateCount(tx *bbolt.Tx, state State, delta int64) error {
	if !knownState(state) {
		return ErrStoreCorrupt
	}
	bucket := tx.Bucket(bucketStateCounts)
	current, ok := decodeUint64(bucket.Get([]byte(state)))
	if !ok {
		return ErrStoreCorrupt
	}
	if delta < 0 {
		decrement := uint64(-delta)
		if current < decrement {
			return ErrStoreCorrupt
		}
		current -= decrement
	} else {
		increment := uint64(delta)
		if current > ^uint64(0)-increment {
			return ErrStoreCorrupt
		}
		current += increment
	}
	if err := bucket.Put([]byte(state), encodeUint64(current)); err != nil {
		return ErrStoreCorrupt
	}
	return nil
}

func moveStateCount(tx *bbolt.Tx, from, to State) error {
	if err := adjustStateCount(tx, from, -1); err != nil {
		return err
	}
	return adjustStateCount(tx, to, 1)
}

func rebuildStateCounts(tx *bbolt.Tx) error {
	counts := make(map[State]uint64, len(AllStates()))
	if err := tx.Bucket(bucketDecisions).ForEach(func(_, value []byte) error {
		var record Record
		if err := decodeRecord(value, &record); err != nil {
			return err
		}
		counts[record.State]++
		return nil
	}); err != nil {
		return err
	}
	for _, state := range AllStates() {
		if err := tx.Bucket(bucketStateCounts).Put([]byte(state), encodeUint64(counts[state])); err != nil {
			return ErrStoreCorrupt
		}
	}
	return nil
}

func encodeKeysetCursor(decisionID string) string {
	return "v1." + base64.RawURLEncoding.EncodeToString([]byte(decisionID))
}

func decodeKeysetCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	if !strings.HasPrefix(cursor, "v1.") {
		return "", invalidf("invalid decision cursor")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(cursor, "v1."))
	if err != nil || len(decoded) == 0 || encodeKeysetCursor(string(decoded)) != cursor {
		return "", invalidf("invalid decision cursor")
	}
	if err := validateText("decision cursor", string(decoded), true); err != nil {
		return "", err
	}
	return string(decoded), nil
}

func subtractCalendarMonths(value time.Time, months int) time.Time {
	value = value.UTC()
	year, month, day := value.Date()
	first := time.Date(year, month-time.Month(months), 1, value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), time.UTC)
	lastDay := first.AddDate(0, 1, -1).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(first.Year(), first.Month(), day, value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), time.UTC)
}

func putRecord(tx *bbolt.Tx, record Record) error {
	if err := putJSON(tx.Bucket(bucketDecisions), []byte(record.Payload.DecisionID), record); err != nil {
		return err
	}
	return tx.Bucket(bucketStateExpiry).Put(stateIndexKey(record), []byte(record.Payload.DecisionID))
}

func decodeRecord(data []byte, record *Record) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(record); err != nil || requireJSONEOF(decoder) != nil {
		return ErrStoreCorrupt
	}
	if err := record.Payload.Validate(); err != nil || record.RecordRevision == 0 || record.StoreRevision == 0 || !knownState(record.State) {
		return ErrStoreCorrupt
	}
	return nil
}

func putAudit(tx *bbolt.Tx, audit TransitionAudit) error {
	return putJSON(tx.Bucket(bucketTransitions), transitionAuditKey(audit.DecisionID, audit.RecordRevision), audit)
}

func transitionAuditKey(decisionID string, revision uint64) []byte {
	return []byte(fmt.Sprintf("%s\x00%020d", decisionID, revision))
}

func putJSON(bucket *bbolt.Bucket, key []byte, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ErrStoreCorrupt
	}
	if err := bucket.Put(key, encoded); err != nil {
		return ErrStoreCorrupt
	}
	return nil
}

func readIdempotency(tx *bbolt.Tx, token, fingerprint string) (idempotencyRecord, bool, error) {
	value := tx.Bucket(bucketIdempotency).Get([]byte(token))
	if value == nil {
		return idempotencyRecord{}, false, nil
	}
	var record idempotencyRecord
	if err := json.Unmarshal(value, &record); err != nil {
		return idempotencyRecord{}, false, ErrStoreCorrupt
	}
	if record.Fingerprint != fingerprint {
		return idempotencyRecord{}, false, ErrIdempotencyConflict
	}
	return record, true, nil
}

func nextStoreRevision(tx *bbolt.Tx) (uint64, error) {
	meta := tx.Bucket(bucketMeta)
	current, ok := decodeUint64(meta.Get(keyStoreRevision))
	if !ok || current == ^uint64(0) {
		return 0, ErrStoreCorrupt
	}
	next := current + 1
	if err := meta.Put(keyStoreRevision, encodeUint64(next)); err != nil {
		return 0, ErrStoreCorrupt
	}
	return next, nil
}

func stateIndexPrefix(state State) []byte {
	return append([]byte(state), 0)
}

func stateIndexKey(record Record) []byte {
	key := stateIndexPrefix(record.State)
	key = append(key, sortableTime(record.Payload.ExpiresAt)...)
	key = append(key, 0)
	key = append(key, record.Payload.DecisionID...)
	return key
}

func stateIndexSeek(state State, at time.Time) []byte {
	key := stateIndexPrefix(state)
	return append(key, sortableTime(at)...)
}

func sortableTime(value time.Time) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value.UTC().UnixNano())^(uint64(1)<<63))
	return encoded[:]
}

func indexDecisionID(key []byte) string {
	first := bytes.IndexByte(key, 0)
	if first < 0 || len(key) <= first+10 {
		return ""
	}
	return string(key[first+10:])
}

func knownState(state State) bool {
	for _, candidate := range AllStates() {
		if state == candidate {
			return true
		}
	}
	return false
}

func fingerprintOf(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func cloneApproval(value *ApprovalPayload) *ApprovalPayload {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneSignature(value *Signature) *Signature {
	if value == nil {
		return nil
	}
	copyValue := *value
	copyValue.Value = append([]byte(nil), value.Value...)
	return &copyValue
}

func encodeUint64(value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	return encoded[:]
}

func decodeUint64(value []byte) (uint64, bool) {
	if len(value) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(value), true
}

func classifyStoreError(err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{ErrStoreLocked, ErrUnsupportedSchema, ErrStoreCorrupt, ErrDecisionNotFound, ErrDecisionExists, ErrStaleRevision, ErrInvalidTransition, ErrIdempotencyConflict, ErrAdmissionCallback, ErrAdmissionCommit, ErrInvalidDecision, ErrAuthorizationRequired, ErrUnknownAuthority, ErrInvalidSignature} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return ErrStoreCorrupt
}
