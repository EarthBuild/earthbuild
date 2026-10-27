package buildkitskipper

import (
	"context"
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

var vertexStateBucket = []byte("vertex-state")

// VertexRecord captures the cache state of one BuildKit vertex from a build run.
type VertexRecord struct {
	ActiveArgs   map[string]string `json:"activeArgs,omitempty"`
	Digest       string
	Operation    string
	BaseImageRef string `json:"baseImageRef,omitempty"`
	Inputs       []string
	CopiedPaths  []string `json:"copiedPaths,omitempty"`
	WasCached    bool
}

// VertexStateStore persists and retrieves the per-vertex cache state for a target across runs.
type VertexStateStore interface {
	// SaveState persists the vertex records for a target after a successful build.
	SaveState(ctx context.Context, target string, records []VertexRecord) error
	// LoadState retrieves the vertex records from the previous build for a target.
	// Returns nil, nil if no prior state exists.
	LoadState(ctx context.Context, target string) ([]VertexRecord, error)
}

// localVertexStateStore is a BoltDB-backed implementation of VertexStateStore.
type localVertexStateStore struct {
	db *bolt.DB
}

// SaveState persists the vertex records for a target after a successful build.
func (s *localVertexStateStore) SaveState(_ context.Context, target string, records []VertexRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("marshal vertex records: %w", err)
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		err := tx.Bucket(vertexStateBucket).Put([]byte(target), data)
		if err != nil {
			return fmt.Errorf("save vertex state for %s: %w", target, err)
		}

		return nil
	})
}

// LoadState retrieves the vertex records from the previous build for a target.
// Returns nil, nil if no prior state exists.
func (s *localVertexStateStore) LoadState(_ context.Context, target string) ([]VertexRecord, error) {
	var records []VertexRecord

	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(vertexStateBucket).Get([]byte(target))
		if data == nil {
			return nil
		}

		return json.Unmarshal(data, &records)
	})
	if err != nil {
		return nil, fmt.Errorf("load vertex state for %s: %w", target, err)
	}

	return records, nil
}
