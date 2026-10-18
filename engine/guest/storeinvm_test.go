package guest_test

import (
	"runtime"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// TestWhereTheStoreLivesWhenNobodySaid.
//
// **The default is per-platform, and the platform is the reason.** On a Mac the
// build runs in a virtual machine and its store is a device inside it: an ext4
// volume, case-sensitive, reached without a virtiofs round trip per file. On a
// machine that builds in a container there is no guest to put a store in, so
// the setting means nothing and stays off.
//
// This is the assertion that the platform files are still there. They are two
// build-tagged constants and nothing else refers to them, so deleting one, or
// dropping the `!darwin` tag, changes the default everywhere and no other test
// would notice - the rest of the suite either opts out explicitly or never
// boots a sandbox at all.
func TestWhereTheStoreLivesWhenNobodySaid(t *testing.T) {
	t.Setenv(guest.EnvStoreInVM, "")

	want := runtime.GOOS == "darwin"
	if got := guest.StoreInVM(); got != want {
		t.Fatalf("with nothing set, the store goes in the guest = %v, want %v on %s"+
			"\n  the default is a per-platform constant: darwin has a guest to"+
			"\n  put a store in, and the other builds have no guest at all",
			got, want, runtime.GOOS)
	}
}

// TestAskingForTheStoreSomewhereElse.
//
// **Empty no longer means off.** It used to: the switch was opt-in, and every
// caller that wanted the old arrangement left the variable unset. Now unset
// means "whatever this platform does", and the nine tests that seed a store by
// hand have to say `0` to get what they used to get from saying nothing.
//
// So the off spellings are load-bearing in a way they were not before, and this
// pins them. `1` is not the only on value - anything that is not an off value
// is on, because a setting that silently ignored `yes` would be worse than one
// that took it.
func TestAskingForTheStoreSomewhereElse(t *testing.T) {
	for _, c := range []struct {
		set  string
		want bool
	}{
		{"0", false},
		{"false", false},
		{"no", false},
		{"1", true},
		{"true", true},
		{"yes", true},
	} {
		t.Run(c.set, func(t *testing.T) {
			t.Setenv(guest.EnvStoreInVM, c.set)
			if got := guest.StoreInVM(); got != c.want {
				t.Fatalf("%s=%q puts the store in the guest = %v, want %v",
					guest.EnvStoreInVM, c.set, got, c.want)
			}
		})
	}
}
