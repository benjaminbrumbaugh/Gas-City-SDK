package routingdecision

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	bbolt "go.etcd.io/bbolt"
)

var (
	bucketExecutionSessions = []byte("execution_sessions_v3")
	bucketExecutionLaunches = []byte("execution_launches_v3")
)

// ExecutionSessionAuthorization is immutable SDK-owned session authority, not
// work metadata. One concrete session cannot migrate to another authorization.
type ExecutionSessionAuthorization struct {
	DecisionID    string           `json:"decision_id"`
	BindingID     string           `json:"binding_id"`
	SessionID     string           `json:"session_id"`
	Generation    string           `json:"generation"`
	InstanceToken string           `json:"instance_token"`
	WorkID        string           `json:"work_id"`
	ClaimFence    int64            `json:"claim_fence"`
	Execution     ExecutionBinding `json:"execution"`
}

// ExecutionLaunchReceipt attests a successful runtime Start operation, not
// inference, provider authentication, work completion, or terminal success.
type ExecutionLaunchReceipt struct {
	ExecutionID   string                        `json:"execution_id"`
	Authorization ExecutionSessionAuthorization `json:"authorization"`
	AttemptID     string                        `json:"attempt_id,omitempty"`
	StartedAt     time.Time                     `json:"started_at"`
}

func validateExecutionAuthorization(tx *bbolt.Tx, auth ExecutionSessionAuthorization) error {
	if auth.SessionID == "" || auth.Generation == "" || auth.InstanceToken == "" {
		return invalidf("execution session identity incomplete")
	}
	value := tx.Bucket(bucketDecisions).Get([]byte(auth.DecisionID))
	if value == nil {
		return ErrDecisionNotFound
	}
	var record Record
	if err := decodeRecord(value, &record); err != nil {
		return err
	}
	p := record.Payload
	if p.Schema != ExecutionSchemaVersion || auth.BindingID != p.BindingID || auth.WorkID != p.WorkBeadID || auth.ClaimFence != p.ClaimFence || p.MatchesExecution(auth.Execution) != nil {
		return invalidf("execution authority binding mismatch")
	}
	if record.State != StateAdmitted && record.State != StateClaimed {
		return ErrInvalidTransition
	}
	return nil
}

// BindExecutionSession commits an immutable authorization before runtime launch.
// Replays are exact; a different tuple/generation/decision fails atomically.
func (store *Store) BindExecutionSession(auth ExecutionSessionAuthorization) error {
	return store.db.Update(func(tx *bbolt.Tx) error {
		if err := validateExecutionAuthorization(tx, auth); err != nil {
			return err
		}
		bucket, err := tx.CreateBucketIfNotExists(bucketExecutionSessions)
		if err != nil {
			return err
		}
		data, err := json.Marshal(auth)
		if err != nil {
			return err
		}
		key := []byte(auth.SessionID)
		if prior := bucket.Get(key); prior != nil {
			if !bytes.Equal(prior, data) {
				return errors.New("execution session migration refused")
			}
			return nil
		}
		return bucket.Put(key, data)
	})
}

// ExecutionSession reads the original durable authorization even if the
// session's mutable trigger, labels, and decision marker have been cleared.
func (store *Store) ExecutionSession(id string) (*ExecutionSessionAuthorization, error) {
	var result *ExecutionSessionAuthorization
	err := store.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketExecutionSessions)
		if bucket == nil {
			return nil
		}
		data := bucket.Get([]byte(id))
		if data == nil {
			return nil
		}
		var auth ExecutionSessionAuthorization
		if json.Unmarshal(data, &auth) != nil || auth.SessionID != id || auth.Execution.Validate() != nil {
			return ErrStoreCorrupt
		}
		result = &auth
		return nil
	})
	return result, err
}

// RecordExecutionLaunch records only a caller-observed successful runtime Start
// under the previously committed exact authorization. Receipt replay is stable.
func (store *Store) RecordExecutionLaunch(auth ExecutionSessionAuthorization) (ExecutionLaunchReceipt, error) {
	return store.RecordExecutionLaunchAttempt(auth, "legacy-replay")
}

// RecordExecutionLaunchAttempt commits one successful Start, keyed by the
// caller-owned attempt token. Retries of that same completion are idempotent.
func (store *Store) RecordExecutionLaunchAttempt(auth ExecutionSessionAuthorization, attemptID string) (ExecutionLaunchReceipt, error) {
	if attemptID == "" {
		return ExecutionLaunchReceipt{}, invalidf("launch attempt identity required")
	}
	var result ExecutionLaunchReceipt
	err := store.db.Update(func(tx *bbolt.Tx) error {
		if err := validateExecutionAuthorization(tx, auth); err != nil {
			return err
		}
		sessions := tx.Bucket(bucketExecutionSessions)
		if sessions == nil {
			return ErrAuthorizationRequired
		}
		data, err := json.Marshal(auth)
		if err != nil {
			return err
		}
		if !bytes.Equal(sessions.Get([]byte(auth.SessionID)), data) {
			return ErrAuthorizationRequired
		}
		hash := sha256.Sum256(append(append([]byte("gascity.execution-launch.v3\x00"), data...), []byte("\x00"+attemptID)...))
		id := "execution_" + hex.EncodeToString(hash[:])
		launches, err := tx.CreateBucketIfNotExists(bucketExecutionLaunches)
		if err != nil {
			return err
		}
		if prior := launches.Get([]byte(id)); prior != nil {
			if json.Unmarshal(prior, &result) != nil {
				return ErrStoreCorrupt
			}
			return nil
		}
		result = ExecutionLaunchReceipt{ExecutionID: id, Authorization: auth, AttemptID: attemptID, StartedAt: store.now().UTC()}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return launches.Put([]byte(id), encoded)
	})
	return result, err
}

// ExecutionLaunches reads durable runtime-start receipts for an exact decision.
// It never treats session liveness, admission, or work closure as a receipt.
func (store *Store) ExecutionLaunches(decisionID string) ([]ExecutionLaunchReceipt, error) {
	result := []ExecutionLaunchReceipt{}
	err := store.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketExecutionLaunches)
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(key, value []byte) error {
			var receipt ExecutionLaunchReceipt
			if json.Unmarshal(value, &receipt) != nil || receipt.ExecutionID != string(key) || receipt.StartedAt.IsZero() || receipt.Authorization.Execution.Validate() != nil {
				return ErrStoreCorrupt
			}
			if receipt.Authorization.DecisionID == decisionID {
				result = append(result, receipt)
			}
			return nil
		})
	})
	return result, err
}
