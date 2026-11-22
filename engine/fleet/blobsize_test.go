package fleet_test

import (
	"bytes"
	"encoding/binary"
	"runtime"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A length a peer sends is a claim, not an allocation.
//
// The prefix is eight bytes the sender chose, and the receiver made a buffer of
// that size before a byte of the body arrived - so a peer could name a few
// gigabytes, send nothing, and the other side paid for them. What is held should
// grow with what actually arrives.
//
//nolint:paralleltest // counts this process's allocations; a parallel test would be counted too
func TestALengthIsNotAnAllocation(t *testing.T) {
	var prefix [8]byte

	binary.BigEndian.PutUint64(prefix[:], 1<<30) // a gigabyte promised

	var buf bytes.Buffer

	buf.Write(prefix[:])
	buf.WriteString("ten bytes!")

	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)

	_, err := fleet.ReadBlobMessage(&buf)

	runtime.ReadMemStats(&after)

	if err == nil {
		t.Error("ten bytes were accepted as a gigabyte")
	}

	if grew := after.TotalAlloc - before.TotalAlloc; grew > 256<<20 {
		t.Errorf("reading ten bytes allocated %d MiB, on the strength of the length alone",
			grew>>20)
	}
}
