package cache_test

import "strconv"

const (
	// testFirst and testSecond are two entries, where only their being distinct
	// matters.
	testFirst  = "one"
	testSecond = "two"
	// testKey is a cache key, chosen for being unremarkable.
	testKey = "test"
)

// byteOf is a loop index as a byte, refusing rather than wrapping.
//
// `byte(i)` on an `int` truncates in silence, which for a fixture means two
// distinct cases quietly becoming one and a test that passes because it stopped
// testing anything (gosec G115). Nothing here uses an index above 255; if
// something starts to, this says so.
func byteOf(i int) byte {
	if i < 0 || i > 255 {
		panic("fixture index out of one byte: " + strconv.Itoa(i))
	}

	return byte(i)
}
