//go:build linux

package exec

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A rebuilt guest is a different guest, and reuse has to notice.
//
// **The digest named the initrd by path.** Rebuild it in place - which is what
// `mkguest -o` does, and what anyone changing the guest agent does all day - and
// the name is unchanged, so a running machine booted from the *old* image
// matches and is handed back. New code on disk, old code in memory, and every
// symptom points at the change that appears not to have taken effect.
//
// Cost me an hour: the layer packer was fixed, the fix was on the box, the
// initrd was rebuilt, and the guest kept packing the old way because it was the
// guest from before.
func TestARebuiltGuestIsNotReused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	at := filepath.Join(dir, "initrd.cpio.gz")

	err := os.WriteFile(at, []byte("the guest"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	before := fileStamp(at)

	// Rebuilt in place, as a build of the guest leaves it.
	err = os.WriteFile(at, []byte("the guest, rebuilt"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if got := fileStamp(at); got == before {
		t.Errorf("a rebuilt initrd stamps the same as the old one: %q", got)
	}

	// And an untouched file stamps the same twice, or every build would refuse
	// to reuse a machine that is perfectly good.
	steady := fileStamp(at)
	if steady != fileStamp(at) {
		t.Error("one unchanged file stamped two ways")
	}
}

// A path that is not there stamps as absent rather than as an error: the
// digest's job is to tell two machines apart, and a missing kernel is a
// failure the boot reports far better than a hash could.
func TestAnAbsentImageStampsAsAbsent(t *testing.T) {
	t.Parallel()

	if got := fileStamp(filepath.Join(t.TempDir(), "no-such")); got == "" {
		t.Error("an absent path stamped as the empty string, which is also what" +
			" an unset path gives - the two must not collide")
	}

	// Distinct from a real file, or an absent kernel would match any other.
	real := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(real, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}

	_ = time.Now()

	if fileStamp(real) == fileStamp(filepath.Join(t.TempDir(), "no-such")) {
		t.Error("a real image and an absent one stamp alike")
	}
}
