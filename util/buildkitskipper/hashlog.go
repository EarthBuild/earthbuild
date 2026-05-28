package buildkitskipper

import (
	"context"
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

var hashLogBucket = []byte("hash-log")

// HashInputRecord is a single labelled input that contributed to a target's
// cache hash. It mirrors inputgraph.HashInput but is defined here to avoid a
// circular import (inputgraph imports buildkitskipper/hasher).
type HashInputRecord struct {
	// Label is a human-readable name for the kind of input (e.g. "ARG", "RUN",
	// "COPY file", "FROM target").
	Label string
	// Detail is additional context such as the expanded value or file path.
	Detail string
}

// HashLogStore persists and retrieves the ordered list of hash inputs for a
// target across runs, enabling Earthfile-level cache miss diffs.
type HashLogStore interface {
	// SaveHashLog persists the hash log for a target after a successful build.
	SaveHashLog(ctx context.Context, target string, log []HashInputRecord) error
	// LoadHashLog retrieves the hash log from the previous build for a target.
	// Returns nil, nil if no prior log exists.
	LoadHashLog(ctx context.Context, target string) ([]HashInputRecord, error)
}

// localHashLogStore is a BoltDB-backed implementation of HashLogStore.
type localHashLogStore struct {
	db *bolt.DB
}

// SaveHashLog persists the hash log for a target.
func (s *localHashLogStore) SaveHashLog(_ context.Context, target string, log []HashInputRecord) error {
	data, err := json.Marshal(log)
	if err != nil {
		return fmt.Errorf("marshal hash log: %w", err)
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		err := tx.Bucket(hashLogBucket).Put([]byte(target), data)
		if err != nil {
			return fmt.Errorf("save hash log for %s: %w", target, err)
		}

		return nil
	})
}

// LoadHashLog retrieves the hash log from the previous build.
// Returns nil, nil if no prior log exists.
func (s *localHashLogStore) LoadHashLog(_ context.Context, target string) ([]HashInputRecord, error) {
	var records []HashInputRecord

	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(hashLogBucket).Get([]byte(target))
		if data == nil {
			return nil
		}

		return json.Unmarshal(data, &records)
	})
	if err != nil {
		return nil, fmt.Errorf("load hash log for %s: %w", target, err)
	}

	return records, nil
}
