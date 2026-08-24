package cli_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// The darwin sandbox says how it shares the store.
//
// `viewsFor` asks for this through an **optional** interface, because three
// sandboxes and every test double would otherwise have to answer a question only
// one of them has an interesting answer to. The cost of an optional interface is
// that forgetting it is silent: the view falls back to the store's own ownership
// and Κ₂ stops serving RUN steps, which is a tier quietly switching itself off
// rather than anything failing (E494).
//
// So the one implementation that must answer is asserted to. **A rule that can
// be forgotten silently needs a test that cannot be.**
//
// The assertion is on the darwin backend, which is the one that shares.
func TestTheDarwinSandboxSaysHowItShares(t *testing.T) {
	t.Parallel()

	var sb exec.Sandbox = &exec.Apple{}

	shared, ok := sb.(interface{ SharesStoreAsRoot() bool })
	if !ok {
		t.Fatal("the darwin sandbox does not say how it shares the store, so" +
			" the view reads the store's own ownership and every observation" +
			" disagrees with it")
	}

	if !shared.SharesStoreAsRoot() {
		t.Error("the darwin sandbox shares the store into a VM as root and" +
			" says it does not")
	}
}
