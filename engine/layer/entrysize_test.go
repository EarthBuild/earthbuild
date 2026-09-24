package layer

import (
	"testing"
	"unsafe"
)

// An entry carries no padding it does not need.
//
// **One of these exists per file in a layer.** The fields were ordered for
// reading - path, mode, uid, gid, mtimeSec, mtimeNs, size, ... - and that scatters
// four `uint32`s among the 64-bit fields, so the compiler paid for each with
// padding: 152 bytes where 144 will do, which on a hundred-thousand-file base is
// eight bytes a hundred thousand times (govet fieldalignment).
//
// Asserted rather than left to the linter, because the linter is advice and this
// is a property: a field added in the wrong place puts the padding back, and the
// next reader of the struct has no way to know that the order is load-bearing
// unless something says so.
//
// If this fails because a field was *added*, the number is meant to move - work
// out the tight order, put the new total here, and say in the commit that the
// entry grew.
func TestAnEntryHasNoPaddingToSpare(t *testing.T) {
	t.Parallel()

	const want = 144

	if got := unsafe.Sizeof(entry{}); got != want {
		t.Errorf("entry is %d bytes, want %d"+
			"\n  the 32-bit fields must sit together or each one costs padding,"+
			"\n  and there is one entry per file in a layer", got, want)
	}
}
