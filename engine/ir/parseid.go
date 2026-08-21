package ir

import (
	"encoding/hex"
	"fmt"
)

// ParseNodeID reads an id back from the form [NodeID.String] writes.
//
// **Refuses rather than pads.** A digest one byte short that parsed to a
// zero-padded id would name a different layer and name it with confidence, so a
// store would answer for content nobody asked for - the failure content
// addressing exists to make impossible.
//
// Here rather than beside each caller: two packages already had a copy of this,
// and a third would be a fourth thing to keep in step with the encoding.
func ParseNodeID(s string) (NodeID, error) {
	var id NodeID

	b, err := hex.DecodeString(s)
	if err != nil {
		return NodeID{}, fmt.Errorf("%q is not a digest: %w", s, err)
	}

	if len(b) != HashSize {
		return NodeID{}, fmt.Errorf("%q is %d bytes, want %d", s, len(b), HashSize)
	}

	copy(id[:], b)

	return id, nil
}
