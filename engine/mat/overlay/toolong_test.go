package overlay

import (
	"strings"
	"testing"
)

// A stack that will not fit says whether the shortening was available.
//
// Lower layers are named through `/proc/self/fd/<n>`, which is eighteen bytes
// and does not vary with the store (E163). Where that is unavailable - no
// `/proc`, or a descriptor this process could not open - the code falls back to
// the path it was given, which is correct and much longer.
//
// **A silent fallback breaks I11**: *"a degradation is always reported with its
// cause"*. Without it the refusal blames the stack - *"the build has to
// flatten"* - when the truth is that a stack of this depth fits perfectly well
// on a machine with /proc, and the reader is sent to restructure their build
// over a missing mount.
func TestAnOverlongStackSaysWhetherShorteningWasAvailable(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", maxMountOptions+1)

	t.Run("shortened, and still too long", func(t *testing.T) {
		t.Parallel()

		err := tooLong(long, 90, true)
		if err == nil {
			t.Fatal("an over-long option string was accepted")
		}

		if strings.Contains(err.Error(), "/proc") {
			t.Errorf("blames the shortening, which was applied:\n%s", err)
		}
	})

	t.Run("not shortened", func(t *testing.T) {
		t.Parallel()

		err := tooLong(long, 40, false)
		if err == nil {
			t.Fatal("an over-long option string was accepted")
		}

		if !strings.Contains(err.Error(), "/proc") {
			t.Errorf("a stack that would have fitted with shortening is blamed on"+
				" its depth, and the cause is not named:\n%s", err)
		}
	})

	t.Run("short enough", func(t *testing.T) {
		t.Parallel()

		err := tooLong("lowerdir=/a", 2, false)
		if err != nil {
			t.Errorf("a string that fits was refused: %v", err)
		}
	})
}
