//go:build linux

package cli

import "testing"

// TestAMicroVMIsTheDefaultWhereThereIsOne.
//
// **This reverses a decision, so it states it.** The setting was offered rather
// than imposed: taking a microVM whenever one was available would change what a
// step can reach, how long the first build waits and where the layers live, on
// every machine, without being asked. What made that the right call was the
// cost - 2.93x the namespace backend on the build this repository does most
// often, most of it a machine booted and taken apart for one build.
//
// That cost is 1.13x now, and the machine is kept. The boundary is worth having
// and is no longer worth asking about, so it is taken where a machine can
// actually be built and the build says so where one cannot.
func TestAMicroVMIsTheDefaultWhereThereIsOne(t *testing.T) {
	for _, c := range []struct {
		set  string
		want bool
		why  string
	}{
		{"", true, "nothing said, and the stronger boundary is the default"},
		{"1", true, "asked for"},
		{"yes", true, "asked for"},
		{"0", false, "declined"},
		{"false", false, "declined"},
		{"no", false, "declined"},
	} {
		t.Setenv(envVM, c.set)

		if got := wantsVM(); got != c.want {
			t.Errorf("%s=%q read as wantsVM=%v, wanted %v (%s)",
				envVM, c.set, got, c.want, c.why)
		}
	}
}

// TestDecliningIsStillPossible states the one direction that has to keep
// working, apart from the table above.
//
// A default nobody can turn off is not a default. `EARTH_VM=0` is how a machine
// that cannot run a guest, or an author bisecting whether the sandbox is what
// broke their build, gets the backend that has always worked.
func TestDecliningIsStillPossible(t *testing.T) {
	t.Setenv(envVM, "0")

	if wantsVM() {
		t.Fatal("a build that asked for no microVM would get one, so there is" +
			" no way back to the namespace backend")
	}
}

// TestAskingIsToldApartFromNotSaying.
//
// **Because the two get different failures.** A build that *asked* for a
// microVM and cannot have one is refused, since running it in namespaces would
// give it a weaker boundary than it believes it has. A build that said nothing
// degrades and is told once. Both of those need to know which happened, and
// `wantsVM` alone cannot say - it answers true for both.
func TestAskingIsToldApartFromNotSaying(t *testing.T) {
	t.Setenv(envVM, "")

	if askedForVM() {
		t.Error("saying nothing reads as asking, so a machine with no guest" +
			" artefacts would refuse the build instead of using namespaces")
	}

	t.Setenv(envVM, "1")

	if !askedForVM() {
		t.Error("asking does not read as asking, so a build that wanted the" +
			" boundary would quietly not get it")
	}
}
