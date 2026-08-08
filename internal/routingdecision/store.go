package routingdecision

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	bbolt "go.etcd.io/bbolt"
)

const (
	// StoreRelativePath is the only controller routing-decision ledger path.
	StoreRelativePath  = ".gc/routing-decisions.db"
	defaultLockTimeout = 250 * time.Millisecond
	maxActiveQuery     = 256
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
	bucketMeta          = []byte("meta")
	bucketDecisions     = []byte("decisions")
	bucketStateExpiry   = []byte("state_expiry")
	bucketIdempotency   = []byte("idempotency")
	bucketTransitions   = []byte("transitions")
	bucketImports       = []byte("imports")
	keySchemaVersion    = []byte("schema_version")
	keyStoreRevision    = []byte("store_revision")
	requiredBucketNames = [][]byte{
		bucketMeta, bucketDecisions, bucketStateExpiry, bucketIdempotency, bucketTransitions, bucketImports,
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
	default:
		return false
	}
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
	Created     *Record            `json:"created,omitempty"`
	Transition  *TransitionReceipt `json:"transition,omitempty"`
}

// FinalAdmission holds the bbolt writer transaction while rechecking the exact
// approved record and signature, running one bounded bead callback, and
// committing admitted/refused lifecycle state. The callback must not reenter
// this store or perform provider/network work.
func (store *Store) FinalAdmission(request FinalAdmissionRequest, verifier Verifier, callback func(Record) (AdmissionCallbackResult, error)) (TransitionReceipt, error) {
	if request.Now.IsZero() {
		request.Now = store.now().UTC()
	} else {
		request.Now = request.Now.UTC()
	}
	var receipt TransitionReceipt
	err := store.db.Update(func(tx *bbolt.Tx) error {
		if strings.TrimSpace(request.DecisionID) == "" || strings.TrimSpace(request.IdempotencyToken) == "" || callback == nil {
			return fmt.Errorf("%w: incomplete final admission", ErrInvalidDecision)
		}
		fingerprint := fingerprintOf(struct {
			Kind    string                `json:"kind"`
			Request FinalAdmissionRequest `json:"request"`
		}{Kind: "admission", Request: request})
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
		result := AdmissionCallbackResult{}
		if !request.Now.Before(record.Payload.ExpiresAt.UTC()) {
			result = AdmissionCallbackResult{State: StateExpired, Reason: "validity window elapsed"}
		} else {
			if request.Now.Before(record.Payload.CreatedAt.UTC()) {
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
		audit := TransitionAudit{DecisionID: record.Payload.DecisionID, From: StateApproved, To: result.State, At: store.now().UTC(), Reason: result.Reason, RecordRevision: record.RecordRevision, StoreRevision: storeRevision}
		if err := putAudit(tx, audit); err != nil {
			return err
		}
		receipt = TransitionReceipt{DecisionID: record.Payload.DecisionID, State: record.State, RecordRevision: record.RecordRevision, StoreRevision: storeRevision}
		idempotency := idempotencyRecord{Kind: "admission", Fingerprint: fingerprint, Transition: &receipt}
		return putJSON(tx.Bucket(bucketIdempotency), []byte(request.IdempotencyToken), idempotency)
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
		for _, name := range requiredBucketNames {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("%w: initialize bucket", ErrStoreCorrupt)
			}
		}
		meta := tx.Bucket(bucketMeta)
		rawSchema := meta.Get(keySchemaVersion)
		if rawSchema == nil {
			if err := meta.Put(keySchemaVersion, encodeUint64(SchemaVersion)); err != nil {
				return err
			}
			if err := meta.Put(keyStoreRevision, encodeUint64(0)); err != nil {
				return err
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
		audit := TransitionAudit{DecisionID: payload.DecisionID, To: StateProposed, At: store.now().UTC(), Reason: "created", RecordRevision: 1, StoreRevision: storeRevision}
		if err := putAudit(tx, audit); err != nil {
			return err
		}
		receipt := idempotencyRecord{Kind: "create", Fingerprint: fingerprint, Created: &result}
		return putJSON(tx.Bucket(bucketIdempotency), []byte(token), receipt)
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
	if strings.TrimSpace(request.DecisionID) == "" || strings.TrimSpace(request.IdempotencyToken) == "" || strings.TrimSpace(request.Reason) == "" {
		return TransitionReceipt{}, fmt.Errorf("%w: incomplete transition", ErrInvalidDecision)
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
	audit := TransitionAudit{DecisionID: record.Payload.DecisionID, From: request.From, To: request.To, At: store.now().UTC(), Reason: request.Reason, RecordRevision: record.RecordRevision, StoreRevision: storeRevision}
	if err := putAudit(tx, audit); err != nil {
		return TransitionReceipt{}, err
	}
	receipt := TransitionReceipt{DecisionID: record.Payload.DecisionID, State: record.State, RecordRevision: record.RecordRevision, StoreRevision: storeRevision}
	idempotency := idempotencyRecord{Kind: "transition", Fingerprint: fingerprint, Transition: &receipt}
	if err := putJSON(tx.Bucket(bucketIdempotency), []byte(request.IdempotencyToken), idempotency); err != nil {
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
	key := []byte(fmt.Sprintf("%s\x00%020d", audit.DecisionID, audit.RecordRevision))
	return putJSON(tx.Bucket(bucketTransitions), key, audit)
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
