package routingdecision

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	bbolt "go.etcd.io/bbolt"
)

const (
	// DeliverySchemaVersion identifies the immutable SDK delivery contract.
	DeliverySchemaVersion = "routing/delivery/v1"
	maxDeliveryQuery      = maxOutcomeQuery
)

var (
	// ErrDeliveryInvalid classifies a malformed or schema-inconsistent item.
	ErrDeliveryInvalid = errors.New("invalid routing outcome delivery")
	// ErrDeliveryConflict classifies a stable delivery identity reused for
	// different immutable bytes or source facts.
	ErrDeliveryConflict = errors.New("routing outcome delivery conflict")
	// ErrDeliveryNotFound classifies an acknowledgement for an unknown item.
	ErrDeliveryNotFound = errors.New("routing outcome delivery not found")
	// ErrDeliveryAckConflict classifies an acknowledgement bound to a different
	// payload digest than the durable item.
	ErrDeliveryAckConflict = errors.New("routing outcome delivery acknowledgement conflict")
	// ErrDeliveryProjection classifies a delivery projection that could not be
	// materialized after its causal lifecycle fact was durably committed.
	ErrDeliveryProjection = errors.New("routing outcome delivery projection failed")
)

// DeliveryItem is an immutable SDK-authored outcome payload awaiting transport
// acknowledgement. Payload is serialized once and returned as typed base64 by
// JSON, so consumers can replay the exact bytes.
type DeliveryItem struct {
	DeliveryID           string `json:"delivery_id"`
	SourceKind           string `json:"source_kind"`
	SourceID             string `json:"source_id"`
	OutcomeSchemaVersion string `json:"outcome_schema_version"`
	OutcomeID            string `json:"outcome_id"`
	RoutingDecisionID    string `json:"routing_decision_id"`
	WorkID               string `json:"work_id"`
	Payload              []byte `json:"payload"`
	PayloadSHA256        string `json:"payload_sha256"`
	EvidenceAtUnix       int64  `json:"evidence_at_unix"`
}

// DeliveryAckRequest binds a transport acknowledgement to one exact item.
type DeliveryAckRequest struct {
	DeliveryID    string `json:"delivery_id"`
	PayloadSHA256 string `json:"payload_sha256"`
}

// DeliveryAck is the durable transport acknowledgement for one immutable item.
type DeliveryAck struct {
	DeliveryID         string `json:"delivery_id"`
	PayloadSHA256      string `json:"payload_sha256"`
	AcknowledgedAtUnix int64  `json:"acknowledged_at_unix"`
}

// DeliveryAckResult reports a newly persisted acknowledgement or an exact
// replay of one already persisted. Replay never regenerates the source item.
type DeliveryAckResult struct {
	Ack    DeliveryAck `json:"ack"`
	Replay bool        `json:"replay"`
}

// DeliveryListOptions controls one bounded delivery-ID keyset page.
type DeliveryListOptions struct {
	Limit  int
	Cursor string
}

// DeliveryPage is one bounded page of unacknowledged immutable delivery items.
type DeliveryPage struct {
	SchemaVersion string         `json:"schema_version"`
	Items         []DeliveryItem `json:"items"`
	NextCursor    string         `json:"next_cursor,omitempty"`
}

// DeliveryIDFor derives a stable item identity from the immutable source fact
// and outcome identity. Callers should use it at each authoritative producer.
func DeliveryIDFor(sourceKind, sourceID, outcomeSchemaVersion, outcomeID string) string {
	hash := sha256.New()
	for _, value := range []string{sourceKind, sourceID, outcomeSchemaVersion, outcomeID} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return "delivery_" + hex.EncodeToString(hash.Sum(nil))
}

// RecordDelivery persists one immutable source item idempotently. A repeated
// exact item is accepted; a reused item or source identity with different
// bytes is rejected.
func (store *Store) RecordDelivery(item DeliveryItem) error {
	if err := validateDeliveryItem(item); err != nil {
		return err
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return ErrDeliveryInvalid
	}
	sourceKey := deliverySourceKey(item.SourceKind, item.SourceID)
	err = store.db.Update(func(tx *bbolt.Tx) error {
		return recordDeliveryTx(tx, item, encoded, sourceKey)
	})
	return classifyStoreError(err)
}

func recordDeliveryTx(tx *bbolt.Tx, item DeliveryItem, encoded, sourceKey []byte) error {
	items := tx.Bucket(bucketDeliveryItems)
	sources := tx.Bucket(bucketDeliverySource)
	if prior := items.Get([]byte(item.DeliveryID)); prior != nil {
		if !bytes.Equal(prior, encoded) {
			return ErrDeliveryConflict
		}
		return nil
	}
	if prior := sources.Get(sourceKey); prior != nil && !bytes.Equal(prior, []byte(item.DeliveryID)) {
		return ErrDeliveryConflict
	}
	if err := items.Put([]byte(item.DeliveryID), encoded); err != nil {
		return ErrStoreCorrupt
	}
	if err := sources.Put(sourceKey, []byte(item.DeliveryID)); err != nil {
		return ErrStoreCorrupt
	}
	return nil
}

func recordExecutionLaunchDeliveryTx(tx *bbolt.Tx, receipt ExecutionLaunchReceipt) error {
	sourceKey := deliverySourceKey("execution-launch", receipt.ExecutionID)
	if prior := tx.Bucket(bucketDeliverySource).Get(sourceKey); prior != nil {
		if tx.Bucket(bucketDeliveryItems).Get(prior) == nil {
			return ErrStoreCorrupt
		}
		// A launch delivery is immutable even after lifecycle reconciliation
		// advances the decision state. Replaying the same receipt must not
		// re-project mutable audit time or status into different bytes.
		return nil
	}
	item, err := decisionWithAuditsTx(tx, receipt.Authorization.DecisionID)
	if err != nil {
		return err
	}
	launches, err := executionLaunchesTx(tx, receipt.Authorization.DecisionID)
	if err != nil {
		return err
	}
	var durableReceipt ExecutionLaunchReceipt
	for _, launch := range launches {
		if launch.ExecutionID == receipt.ExecutionID {
			durableReceipt = launch
			break
		}
	}
	if durableReceipt.ExecutionID == "" {
		return ErrStoreCorrupt
	}
	row, available, err := ProjectProducerExecutionOutcome(item, []ExecutionLaunchReceipt{durableReceipt})
	if err != nil {
		return err
	}
	if !available || row.RoutingDecisionID == nil {
		return ErrStoreCorrupt
	}
	payload, err := json.Marshal(row)
	if err != nil {
		return ErrStoreCorrupt
	}
	itemToStore := DeliveryItem{
		SourceKind:           "execution-launch",
		SourceID:             receipt.ExecutionID,
		OutcomeSchemaVersion: row.SchemaVersion,
		OutcomeID:            row.OutcomeID,
		RoutingDecisionID:    *row.RoutingDecisionID,
		WorkID:               row.WorkID,
		Payload:              payload,
		PayloadSHA256:        payloadDigest(payload),
		EvidenceAtUnix:       row.ObservedAtUnix,
	}
	itemToStore.DeliveryID = DeliveryIDFor(itemToStore.SourceKind, itemToStore.SourceID, itemToStore.OutcomeSchemaVersion, itemToStore.OutcomeID)
	encoded, err := json.Marshal(itemToStore)
	if err != nil {
		return ErrStoreCorrupt
	}
	return recordDeliveryTx(tx, itemToStore, encoded, sourceKey)
}

func (store *Store) recordExecutionLaunchDelivery(receipt ExecutionLaunchReceipt) error {
	err := store.db.Update(func(tx *bbolt.Tx) error {
		return recordExecutionLaunchDeliveryTx(tx, receipt)
	})
	if errors.Is(err, ErrStoreCorrupt) || errors.Is(err, ErrDecisionNotFound) {
		return classifyStoreError(err)
	}
	if err != nil {
		return classifyStoreError(fmt.Errorf("%w: %w", ErrDeliveryProjection, err))
	}
	return nil
}

func (store *Store) recordNonAdmissionDelivery(decisionID string) error {
	err := store.db.Update(func(tx *bbolt.Tx) error {
		value := tx.Bucket(bucketDecisions).Get([]byte(decisionID))
		if value == nil {
			return ErrDecisionNotFound
		}
		var record Record
		if err := decodeRecord(value, &record); err != nil {
			return err
		}
		if !IsTerminalState(record.State) {
			return nil
		}
		audit, err := latestAudit(tx, record)
		if err != nil {
			return err
		}
		return recordNonAdmissionDeliveryTx(tx, record, audit)
	})
	if errors.Is(err, ErrStoreCorrupt) || errors.Is(err, ErrDecisionNotFound) {
		return classifyStoreError(err)
	}
	if err != nil {
		return classifyStoreError(fmt.Errorf("%w: %w", ErrDeliveryProjection, err))
	}
	return nil
}

func recordNonAdmissionDeliveryTx(tx *bbolt.Tx, record Record, audit TransitionAudit) error {
	if record.State != StateRefusedAfterRace && record.State != StateExpired && record.State != StateRevoked {
		return nil
	}
	// Older signed decisions may legitimately omit the recommendation identity;
	// without that causal field there is no portable v2/v3 delivery to persist.
	if strings.TrimSpace(record.Payload.RecommendationID) == "" {
		return nil
	}
	if record.Payload.Schema != ExecutionSchemaVersion && !validRecommendationID(record.Payload.RecommendationID) {
		return nil
	}
	sourceID := record.Payload.DecisionID + ":" + strconv.FormatUint(audit.RecordRevision, 10) + ":" + strconv.FormatUint(audit.StoreRevision, 10) + ":" + string(record.State)
	var (
		outcomeSchema string
		outcomeID     string
		decisionID    string
		workID        string
		observedAt    int64
		payload       []byte
	)
	item := DecisionWithAudits{Record: record, Audits: []TransitionAudit{audit}}
	if record.Payload.Schema == ExecutionSchemaVersion {
		launches, err := executionLaunchesTx(tx, record.Payload.DecisionID)
		if err != nil {
			return err
		}
		if len(launches) > 0 {
			return nil
		}
		row, available, err := ProjectProducerExecutionOutcome(item, nil)
		if err != nil || !available || row.RoutingDecisionID == nil {
			if err != nil {
				return err
			}
			return ErrStoreCorrupt
		}
		outcomeSchema, outcomeID, decisionID, workID, observedAt = row.SchemaVersion, row.OutcomeID, *row.RoutingDecisionID, row.WorkID, row.ObservedAtUnix
		payload, err = json.Marshal(row)
		if err != nil {
			return ErrStoreCorrupt
		}
	} else {
		row := ProjectOutcome(item, OutcomeWorkSnapshot{}, OutcomeAuthoritySnapshot{}, time.Time{})
		if err := row.Validate(); err != nil {
			return err
		}
		outcomeSchema, outcomeID, decisionID, workID, observedAt = row.SchemaVersion, row.OutcomeID, record.Payload.DecisionID, row.WorkID, row.ObservedAtUnix
		var err error
		payload, err = json.Marshal(row)
		if err != nil {
			return ErrStoreCorrupt
		}
	}
	itemToStore := DeliveryItem{
		SourceKind: "routing-terminal-transition", SourceID: sourceID,
		OutcomeSchemaVersion: outcomeSchema, OutcomeID: outcomeID,
		RoutingDecisionID: decisionID, WorkID: workID, Payload: payload,
		PayloadSHA256: payloadDigest(payload), EvidenceAtUnix: observedAt,
	}
	itemToStore.DeliveryID = DeliveryIDFor(itemToStore.SourceKind, itemToStore.SourceID, itemToStore.OutcomeSchemaVersion, itemToStore.OutcomeID)
	encoded, err := json.Marshal(itemToStore)
	if err != nil {
		return ErrStoreCorrupt
	}
	return recordDeliveryTx(tx, itemToStore, encoded, deliverySourceKey(itemToStore.SourceKind, itemToStore.SourceID))
}

func decisionWithAuditsTx(tx *bbolt.Tx, decisionID string) (DecisionWithAudits, error) {
	value := tx.Bucket(bucketDecisions).Get([]byte(decisionID))
	if value == nil {
		return DecisionWithAudits{}, ErrDecisionNotFound
	}
	var record Record
	if err := decodeRecord(value, &record); err != nil {
		return DecisionWithAudits{}, err
	}
	audits, err := auditsForDecision(tx, decisionID)
	if err != nil {
		return DecisionWithAudits{}, err
	}
	receiptID, err := admissionReceiptIDForDecision(tx, decisionID)
	if err != nil {
		return DecisionWithAudits{}, err
	}
	return DecisionWithAudits{Record: record, Audits: audits, AdmissionReceiptID: receiptID}, nil
}

func executionLaunchesTx(tx *bbolt.Tx, decisionID string) ([]ExecutionLaunchReceipt, error) {
	result := []ExecutionLaunchReceipt{}
	bucket := tx.Bucket(bucketExecutionLaunches)
	if bucket == nil {
		return result, nil
	}
	err := bucket.ForEach(func(key, value []byte) error {
		var receipt ExecutionLaunchReceipt
		if json.Unmarshal(value, &receipt) != nil || receipt.ExecutionID != string(key) || receipt.StartedAt.IsZero() || receipt.Authorization.Execution.Validate() != nil {
			return ErrStoreCorrupt
		}
		if receipt.Authorization.DecisionID == decisionID {
			result = append(result, receipt)
		}
		return nil
	})
	return result, err
}

func payloadDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// ListPendingDeliveries returns a bounded stable-ID page of items without a
// durable acknowledgement. The stored payload and timestamp are returned
// unchanged; only the caller's slice is newly allocated by JSON decoding.
func (store *Store) ListPendingDeliveries(options DeliveryListOptions) (DeliveryPage, error) {
	if options.Limit <= 0 || options.Limit > maxDeliveryQuery {
		return DeliveryPage{}, invalidf("delivery list limit must be between 1 and %d", maxDeliveryQuery)
	}
	after, err := decodeKeysetCursor(options.Cursor)
	if err != nil {
		return DeliveryPage{}, err
	}
	page := DeliveryPage{SchemaVersion: DeliverySchemaVersion, Items: make([]DeliveryItem, 0, options.Limit)}
	err = store.db.View(func(tx *bbolt.Tx) error {
		items, acks := tx.Bucket(bucketDeliveryItems), tx.Bucket(bucketDeliveryAcks)
		cursor := items.Cursor()
		key, value := cursor.First()
		if after != "" {
			key, value = cursor.Seek([]byte(after))
			if key != nil && string(key) == after {
				key, value = cursor.Next()
			}
		}
		scanned := 0
		lastScanned := ""
		for key != nil && scanned < maxDeliveryQuery && len(page.Items) < options.Limit {
			var item DeliveryItem
			if strictUnmarshal(value, &item) != nil || string(key) != item.DeliveryID {
				return ErrStoreCorrupt
			}
			if err := validateDeliveryItem(item); err != nil {
				return fmt.Errorf("%w: stored item %q: %w", ErrStoreCorrupt, item.DeliveryID, err)
			}
			scanned++
			lastScanned = string(key)
			if ack := acks.Get(key); ack == nil {
				page.Items = append(page.Items, item)
			} else {
				var stored DeliveryAck
				if strictUnmarshal(ack, &stored) != nil || stored.DeliveryID != item.DeliveryID || stored.PayloadSHA256 != item.PayloadSHA256 || stored.AcknowledgedAtUnix <= 0 {
					return ErrStoreCorrupt
				}
			}
			key, value = cursor.Next()
		}
		if key != nil && lastScanned != "" {
			page.NextCursor = encodeKeysetCursor(lastScanned)
		}
		return nil
	})
	return page, classifyStoreError(err)
}

// AcknowledgeDelivery durably acknowledges one item by exact payload digest.
// Repeating the same request returns the original acknowledgement; a missing
// item or different digest cannot be interpreted as successful delivery.
func (store *Store) AcknowledgeDelivery(request DeliveryAckRequest) (DeliveryAckResult, error) {
	if err := validateDeliveryAckRequest(request); err != nil {
		return DeliveryAckResult{}, err
	}
	var result DeliveryAckResult
	err := store.db.Update(func(tx *bbolt.Tx) error {
		items, acks := tx.Bucket(bucketDeliveryItems), tx.Bucket(bucketDeliveryAcks)
		value := items.Get([]byte(request.DeliveryID))
		if value == nil {
			return ErrDeliveryNotFound
		}
		var item DeliveryItem
		if strictUnmarshal(value, &item) != nil {
			return ErrStoreCorrupt
		}
		if err := validateDeliveryItem(item); err != nil {
			return ErrStoreCorrupt
		}
		if item.PayloadSHA256 != request.PayloadSHA256 {
			return ErrDeliveryAckConflict
		}
		if prior := acks.Get([]byte(request.DeliveryID)); prior != nil {
			var ack DeliveryAck
			if strictUnmarshal(prior, &ack) != nil || ack.DeliveryID != item.DeliveryID || ack.PayloadSHA256 != item.PayloadSHA256 || ack.AcknowledgedAtUnix <= 0 {
				return ErrStoreCorrupt
			}
			result = DeliveryAckResult{Ack: ack, Replay: true}
			return nil
		}
		ack := DeliveryAck{DeliveryID: item.DeliveryID, PayloadSHA256: item.PayloadSHA256, AcknowledgedAtUnix: store.now().UTC().Unix()}
		if ack.AcknowledgedAtUnix <= 0 {
			return ErrStoreCorrupt
		}
		if err := putJSON(acks, []byte(item.DeliveryID), ack); err != nil {
			return err
		}
		result = DeliveryAckResult{Ack: ack}
		return nil
	})
	return result, classifyStoreError(err)
}

func deliverySourceKey(sourceKind, sourceID string) []byte {
	return []byte(sourceKind + "\x00" + sourceID)
}

func validateDeliveryAckRequest(request DeliveryAckRequest) error {
	if err := validateText("delivery id", request.DeliveryID, true); err != nil {
		return fmt.Errorf("%w: %w", ErrDeliveryInvalid, err)
	}
	if err := validatePayloadDigest(request.PayloadSHA256, nil); err != nil {
		return err
	}
	return nil
}

func validateDeliveryItem(item DeliveryItem) error {
	for name, value := range map[string]string{
		"delivery id": item.DeliveryID, "source kind": item.SourceKind, "source id": item.SourceID,
		"outcome schema version": item.OutcomeSchemaVersion, "outcome id": item.OutcomeID,
		"routing decision id": item.RoutingDecisionID, "work id": item.WorkID,
	} {
		if err := validateText(name, value, true); err != nil {
			return fmt.Errorf("%w: %w", ErrDeliveryInvalid, err)
		}
	}
	if item.EvidenceAtUnix <= 0 || len(item.Payload) == 0 {
		return fmt.Errorf("%w: missing evidence or payload", ErrDeliveryInvalid)
	}
	if err := validatePayloadDigest(item.PayloadSHA256, item.Payload); err != nil {
		return err
	}
	if DeliveryIDFor(item.SourceKind, item.SourceID, item.OutcomeSchemaVersion, item.OutcomeID) != item.DeliveryID {
		return fmt.Errorf("%w: delivery identity does not match source", ErrDeliveryInvalid)
	}
	return validateDeliveryPayload(item)
}

func validatePayloadDigest(value string, payload []byte) error {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return fmt.Errorf("%w: invalid payload digest", ErrDeliveryInvalid)
	}
	digest := strings.TrimPrefix(value, "sha256:")
	if !validDigest(digest) {
		return fmt.Errorf("%w: invalid payload digest", ErrDeliveryInvalid)
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("%w: invalid payload digest", ErrDeliveryInvalid)
	}
	if payload != nil {
		expected := sha256.Sum256(payload)
		if !bytes.Equal(decoded, expected[:]) {
			return fmt.Errorf("%w: payload digest mismatch", ErrDeliveryInvalid)
		}
	}
	return nil
}

func validateDeliveryPayload(item DeliveryItem) error {
	switch item.OutcomeSchemaVersion {
	case OutcomeSchemaVersion:
		var outcome OutcomeRecord
		if err := strictUnmarshal(item.Payload, &outcome); err != nil || outcome.Validate() != nil || outcome.SchemaVersion != item.OutcomeSchemaVersion || outcome.OutcomeID != item.OutcomeID || outcome.WorkID != item.WorkID || outcome.RoutingDecisionID == nil || *outcome.RoutingDecisionID != item.RoutingDecisionID {
			return fmt.Errorf("%w: invalid v2 outcome payload", ErrDeliveryInvalid)
		}
	case "routing/outcome/v3":
		var outcome ProducerExecutionOutcome
		if err := strictUnmarshal(item.Payload, &outcome); err != nil || !validProducerDeliveryOutcome(outcome) || outcome.SchemaVersion != item.OutcomeSchemaVersion || outcome.OutcomeID != item.OutcomeID || outcome.WorkID != item.WorkID || outcome.RoutingDecisionID == nil || *outcome.RoutingDecisionID != item.RoutingDecisionID || outcome.ObservedAtUnix <= 0 {
			return fmt.Errorf("%w: invalid v3 outcome payload", ErrDeliveryInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported outcome schema", ErrDeliveryInvalid)
	}
	return nil
}

func validProducerDeliveryOutcome(outcome ProducerExecutionOutcome) bool {
	if outcome.SchemaVersion != "routing/outcome/v3" || outcome.ObservedAtUnix <= 0 || outcome.OutcomeID == "" || !strings.HasPrefix(outcome.OutcomeID, "outcome_") || len(outcome.OutcomeID) != len("outcome_")+64 {
		return false
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(outcome.OutcomeID, "outcome_")); err != nil {
		return false
	}
	canonicalID, err := producerExecutionOutcomeID(outcome)
	if err != nil || canonicalID != outcome.OutcomeID {
		return false
	}
	for name, value := range map[string]string{
		"outcome_id": outcome.OutcomeID, "correlation_id": outcome.CorrelationID, "recommendation_id": outcome.RecommendationID,
		"work_id": outcome.WorkID, "requested_target_id": outcome.RequestedTargetID, "provenance": outcome.Provenance,
	} {
		if validateOutcomeOpaque(name, value) != nil {
			return false
		}
	}
	if outcome.RoutingDecisionID == nil || validateOutcomeOpaque("routing_decision_id", *outcome.RoutingDecisionID) != nil || !validPortableDigest(outcome.RequestedConfigDigest) {
		return false
	}
	if (outcome.ActualTargetID == nil) != (outcome.ActualConfigDigest == nil) {
		return false
	}
	if outcome.ActualTargetID != nil && (validateOutcomeOpaque("actual_target_id", *outcome.ActualTargetID) != nil || !validPortableDigest(*outcome.ActualConfigDigest)) {
		return false
	}
	knownDisposition := outcome.Disposition == OutcomeDispositionShipped || outcome.Disposition == OutcomeDispositionNoOp || outcome.Disposition == OutcomeDispositionBlocked || outcome.Disposition == OutcomeDispositionAbandoned || outcome.Disposition == OutcomeDispositionNotAdmitted || outcome.Disposition == OutcomeDispositionUnknown
	knownFailure := outcome.FailureClass == OutcomeFailureNone || outcome.FailureClass == OutcomeFailureTransient || outcome.FailureClass == OutcomeFailureHard || outcome.FailureClass == OutcomeFailureUnknown
	return knownDisposition && knownFailure && outcome.Status != ""
}
