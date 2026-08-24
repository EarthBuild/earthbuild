//go:build linux && integration

package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// A step resolves by the entries its Earthfile declared.
//
// The whole point of `HOST`, and the only way to know it works: a plan that
// carries the entries and a guest that writes a file are two mechanisms
// agreeing with each other. This asks the step.
//
// The prober is this binary, copied in and re-executed - the trick the daemon
// tests use, because a step's root is empty and a Go toolchain is not something
// a CI image should need (E387).
func TestAStepResolvesByItsDeclaredHosts(t *testing.T) {
	if !guest.NeedsIsolation(t) {
		return
	}

	root := stepRoot(t)

	self, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, "prober"), self, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = h.Release() })

	step, err := c.RunStep(context.Background(), h, guest.Step{
		Argv:  []string{"/prober", "--earthbuild-test-resolve", "api.test"},
		Hosts: []string{"api.test 10.1.2.3"},
	}, nil)
	if err != nil {
		t.Fatalf("the step could not be run: %v", err)
	}

	if step.Exit != 0 {
		t.Fatalf("the step did not resolve the name it was given (exit %d):\n%s", step.Exit, step.Output)
	}

	if !strings.Contains(step.Output, "10.1.2.3") {
		t.Errorf("the name resolved to something else:\n%s", step.Output)
	}
}
