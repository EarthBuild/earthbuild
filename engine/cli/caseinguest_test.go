package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

// No case-sensitivity advice about directories nothing is unpacked into.
//
// The note exists because unpacking a layer on a case-insensitive filesystem
// collides two names that differ only in case, and macOS is case-insensitive by
// default - APFS ships in two flavours and the installer picks that one.
//
// It is advice about *where an image is unpacked*. With the unpack in the guest,
// which a store on the guest's own device implies, that is no longer this
// machine: the guest's volume is ext4, and `container volume create` followed by
// `touch Foo.txt` shows `foo.txt` absent. So the case this warns about cannot
// arise there, and warning anyway is the true-and-irrelevant paragraph E491
// removed, arriving by another route.
//
// One rule, one place: `exec.UnpacksInGuest` answers it for the unpack routing
// as well, because a second spelling of the same question is how two answers
// come to disagree.
func TestNoCaseAdviceWhenTheGuestUnpacks(t *testing.T) {
	// A directory that really is case-insensitive, so the note has every
	// reason to be produced and only the guest's unpack stops it. On a
	// case-sensitive machine there is nothing to suppress and the assertion
	// below is vacuous, which is worth saying rather than hiding.
	dir := t.TempDir()
	if caseSensitiveStore(dir) {
		t.Skip("this filesystem is case-sensitive, so there is no note to withhold")
	}

	if caseNoteFor(cacheDir{path: dir, env: envCacheDir}) == "" {
		t.Fatal("no note for a case-insensitive store with the unpack here:" +
			" the rest of this test would pass for the wrong reason")
	}

	t.Setenv(guest.EnvStoreInVM, "1")

	if got := caseNoteFor(cacheDir{path: dir, env: envCacheDir}); got != "" {
		t.Errorf("a store on the guest's device still produced advice about a"+
			" host directory nothing is unpacked into:\n%s", got)
	}

	if !exec.UnpacksInGuest() {
		t.Fatal("a store on the guest's device does not imply the guest" +
			" unpacks: the host cannot write a block device it does not have")
	}

	t.Setenv(guest.EnvStoreInVM, "")
	t.Setenv(exec.EnvUnpackInGuest, "1")

	if !exec.UnpacksInGuest() {
		t.Error("asking for the unpack in the guest did not move it")
	}

	t.Setenv(exec.EnvUnpackInGuest, "")

	if exec.UnpacksInGuest() {
		t.Error("the unpack moved into the guest with neither switch set:" +
			" every ordinary build on a mac would stop being warned about a" +
			" case-insensitive store that is still the one it uses")
	}
}
