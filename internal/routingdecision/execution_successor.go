package routingdecision

import (
	"bytes"
	"encoding/json"
	"strconv"

	bbolt "go.etcd.io/bbolt"
)

var bucketExecutionSuccessors = []byte("execution_successors_v3")

func executionIncarnationKey(auth ExecutionSessionAuthorization) []byte {
	data, _ := json.Marshal([]string{auth.SessionID, auth.Generation, auth.InstanceToken})
	return data
}

// ExecutionSessionIncarnation requires an exact original or controller-issued
// successor. Mutable session metadata cannot manufacture authority.
func (store *Store) ExecutionSessionIncarnation(id, generation, token string) (*ExecutionSessionAuthorization, error) {
	original, err := store.ExecutionSession(id)
	if err != nil || original == nil {
		return original, err
	}
	expected := *original
	expected.Generation, expected.InstanceToken = generation, token
	encoded, err := json.Marshal(expected)
	if err != nil {
		return nil, err
	}
	err = store.db.View(func(tx *bbolt.Tx) error {
		if generation != original.Generation || token != original.InstanceToken {
			bucket := tx.Bucket(bucketExecutionSuccessors)
			if bucket == nil {
				return ErrAuthorizationRequired
			}
			data := bucket.Get(executionIncarnationKey(expected))
			if data == nil {
				return ErrAuthorizationRequired
			}
			if !bytes.Equal(data, encoded) {
				return ErrStoreCorrupt
			}
		}
		return checkExecutionIncarnationHead(tx, expected)
	})
	if err != nil {
		return nil, err
	}
	return &expected, nil
}

var bucketExecutionHeads = []byte("execution_heads_v3")

// A successful successor exec prevents rollback to an earlier incarnation.
// Unused controller-issued tokens remain retryable when metadata or exec fails.
func checkExecutionIncarnationHead(tx *bbolt.Tx, auth ExecutionSessionAuthorization) error {
	bucket := tx.Bucket(bucketExecutionHeads)
	if bucket == nil {
		return nil
	}
	data := bucket.Get([]byte(auth.SessionID))
	if data == nil {
		return nil
	}
	var head ExecutionSessionAuthorization
	if json.Unmarshal(data, &head) != nil {
		return ErrStoreCorrupt
	}
	headGen, err := strconv.Atoi(head.Generation)
	if err != nil {
		return ErrStoreCorrupt
	}
	gen, err := strconv.Atoi(auth.Generation)
	if err != nil {
		return ErrAuthorizationRequired
	}
	if gen < headGen || (gen == headGen && auth.InstanceToken != head.InstanceToken) {
		return ErrAuthorizationRequired
	}
	originalTuple := auth
	originalTuple.Generation, originalTuple.InstanceToken = head.Generation, head.InstanceToken
	encoded, err := json.Marshal(originalTuple)
	if err != nil || !bytes.Equal(data, encoded) {
		return ErrStoreCorrupt
	}
	return nil
}

// AuthorizeExecutionSuccessor appends an exact successor under the original
// claimed decision. The controller must verify the original-work claim fence
// and final local tuple before calling. Interrupted session writes leave only
// unused controller-issued tokens, never mutable-metadata launch authority.
func (store *Store) AuthorizeExecutionSuccessor(prior ExecutionSessionAuthorization, generation, token string) error {
	current, err := store.ExecutionSessionIncarnation(prior.SessionID, prior.Generation, prior.InstanceToken)
	if err != nil || current == nil {
		return ErrAuthorizationRequired
	}
	a, _ := json.Marshal(prior)
	b, _ := json.Marshal(current)
	if !bytes.Equal(a, b) {
		return ErrAuthorizationRequired
	}
	oldGen, err := strconv.Atoi(prior.Generation)
	if err != nil || generation != strconv.Itoa(oldGen+1) || token == "" || token == prior.InstanceToken {
		return ErrAuthorizationRequired
	}
	successor := prior
	successor.Generation, successor.InstanceToken = generation, token
	return store.db.Update(func(tx *bbolt.Tx) error {
		if err := validateExecutionAuthorization(tx, successor); err != nil {
			return err
		}
		var record Record
		if err := decodeRecord(tx.Bucket(bucketDecisions).Get([]byte(prior.DecisionID)), &record); err != nil {
			return err
		}
		if record.State != StateClaimed {
			return ErrAuthorizationRequired
		}
		bucket, err := tx.CreateBucketIfNotExists(bucketExecutionSuccessors)
		if err != nil {
			return err
		}
		data, err := json.Marshal(successor)
		if err != nil {
			return err
		}
		key := executionIncarnationKey(successor)
		if existing := bucket.Get(key); existing != nil && !bytes.Equal(existing, data) {
			return ErrAuthorizationRequired
		}
		return bucket.Put(key, data)
	})
}
