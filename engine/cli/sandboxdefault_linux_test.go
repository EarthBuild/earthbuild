//go:build linux && integration

package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// Not asked for, it is a microVM where one can be had and namespaces where one
// cannot.
//
// **The default changed and this test did not**, so it asserted the namespace
// backend on a machine that had stopped choosing it and was left failing. The
// property worth pinning is not which of the two answers comes back - that is
// the machine's to decide - but that the choice is made silently: a build that
// said nothing gets a working build either way.
//
// **An integration test, because of what it needs rather than what it asserts.**
// Choosing a backend at all requires the agent that runs inside one: with no
// `earth-guestd` beside the binary there is no sandbox to pick, and this said
// so - "saying nothing left this machine with no sandbox at all: cannot find
// earth-guestd". Its siblings in sandboxvm_linux_test.go name a backend
// explicitly and are answered without probing, so they remain unit tests; this
// one exercises the probe, which is the part that needs a built tree.
//
// Left failing in `+unit-test`, it was a red suite that told nobody anything:
// the harness copies source and does not build the agent, so the test could
// never pass there however correct the engine was.
func TestTheDefaultIsAMicroVMWhereThereCanBeOne(t *testing.T) { // not parallel: sets the environment
	t.Setenv(envVM, "")

	sb, err := sandbox("")
	if err != nil {
		t.Fatal("saying nothing left this machine with no sandbox at all: ", err)
	}

	switch sb.(type) {
	case *exec.Firecracker, *exec.Native:
	default:
		t.Errorf("the default backend is %T, which is neither", sb)
	}
}
