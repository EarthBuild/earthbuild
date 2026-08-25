package exec

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// TestALocalContextSaysWhyItCannotBeStagedIntoTheGuestsStore.
//
// **What is left of the refusal now the context can be handed across.** A
// sandbox that can say where the guest sees a host path packs the context and
// gives it to the guest to place; one that cannot has no route at all, and this
// is what it says rather than failing later as a missing artifact.
//
// **A comment held an assumption that a later switch falsified.** `OpLocal`
// reads "the context lives on the host, and so does the store, so this is a
// host-side copy: nothing needs to enter the sandbox to do it" - true when it
// was written, and untrue the moment `EARTH_STORE_IN_VM` moved the store onto
// the guest's own device.
//
// What the user saw was `COPY src: nothing in that target has it`: the context
// was staged into a store the guest cannot read, so the guest looked through
// the layers it *does* have, found nothing, and reported the only thing it
// could see - a missing artifact, naming a target nobody wrote.
//
// Refusing is not the fix; it is what I10 asks for until the fix exists. An
// engine that cannot evaluate a construct says so, names it, and never
// approximates - because a wrong answer that looks like a build is worse than
// no build.
func TestALocalContextSaysWhyItCannotBeStagedIntoTheGuestsStore(t *testing.T) {
	t.Setenv(guest.EnvStoreInVM, "1")

	err := localContextRefusal()
	if err == nil {
		t.Fatal("a sandbox with no route for the context accepted one anyway," +
			"\n  and it cannot work: the layer lands where the guest cannot read" +
			"\n  it, and the failure surfaces as a missing artifact")
	}

	// The message has to name the cause and the way out, or it sends a reader
	// hunting for a target that was never written.
	for _, want := range []string{guest.EnvStoreInVM, "COPY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n  %v", want, err)
		}
	}
}

// And with the store where the context is, nothing is refused.
func TestALocalContextIsFineWhenTheStoreIsOnTheHost(t *testing.T) {
	t.Setenv(guest.EnvStoreInVM, "")

	err := localContextRefusal()
	if err != nil {
		t.Errorf("a local context was refused with the store on the host: %v", err)
	}
}
