package core_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step's Command carries the parts a remote execution API has a field for.
//
// **The argv is the argv.** Nothing is prefixed, wrapped or namespaced, because
// a client this engine did not write - buck2 - declares actions and expects
// `arguments` to be a command line. What that API has no field for rides in the
// platform instead, so a plain RUN looks like a plain action.
func TestAStepsCommandIsItsCommandLine(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op: ir.Op{
			Kind: ir.OpExec,
			Args: []string{"/bin/sh", "-c", "cargo build"},
			Dir:  "/w",
			Env:  map[string]string{"PATH": "/usr/bin", "CARGO_TERM_COLOR": "never"},
		},
		Platform: ir.Platform{OS: "linux", Arch: "arm64"},
	}

	cmd := core.CommandOf(n)

	if strings.Join(cmd.Arguments, " ") != "/bin/sh -c cargo build" {
		t.Errorf("arguments are %q, and should be the command line itself", cmd.Arguments)
	}

	if cmd.WorkingDirectory != "/w" {
		t.Errorf("working directory is %q", cmd.WorkingDirectory)
	}

	// REAPI requires environment variables in name order.
	var last string

	for _, e := range cmd.Env {
		if e.Name < last {
			t.Errorf("environment variables are not in name order: %q after %q", e.Name, last)
		}

		last = e.Name
	}

	if len(cmd.Env) != 2 {
		t.Errorf("%d environment variables, want 2", len(cmd.Env))
	}
}

// The platform carries the machine, and one digest for everything else.
//
// **Not twenty-five named properties.** Every field of an operation must reach
// the key - TestEveryOperationFieldReachesTheKey enforces that by reflection -
// and most have no REAPI field at all. Exploding each into a string would be a
// second encoding of the operation to keep in step with the first. One digest
// over the rest is total, injective, and adds nothing to maintain.
func TestThePlatformCarriesTheMachineAndOneDigestForTheRest(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op:       ir.Op{Kind: ir.OpExec, Args: []string{"x"}},
		Platform: ir.Platform{OS: "linux", Arch: "arm64"},
	}

	props := core.PlatformOf(n, nil)

	want := map[string]string{"arch": "arm64", "os": "linux"}

	var rest string

	for _, p := range props {
		if p.Name == "earthbuild.operation" {
			rest = p.Value

			continue
		}

		if want[p.Name] != p.Value {
			t.Errorf("platform property %q is %q, want %q", p.Name, p.Value, want[p.Name])
		}

		delete(want, p.Name)
	}

	for name := range want {
		t.Errorf("the platform does not name %q", name)
	}

	if len(rest) != 2*ir.HashSize {
		t.Errorf("earthbuild.operation is %q, want a digest", rest)
	}

	// Sorted, which REAPI requires and a digest depends on.
	for i := 1; i < len(props); i++ {
		if props[i].Name < props[i-1].Name {
			t.Errorf("platform properties are not in name order: %q after %q",
				props[i].Name, props[i-1].Name)
		}
	}
}

// Changing anything about an operation changes the one digest that carries it.
//
// The reflection guard checks this of the key; this checks it of the property
// the key now goes through, so a field lost on the way into the platform is
// caught where it happens rather than three layers up.
func TestEveryOperationFieldReachesThePlatformDigest(t *testing.T) {
	t.Parallel()

	base := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"x"}}}
	was := operationDigest(t, core.PlatformOf(base, nil))

	for _, n := range []*ir.Node{
		{Op: ir.Op{Kind: ir.OpExec, Args: []string{"x"}, Privileged: true}},
		{Op: ir.Op{Kind: ir.OpExec, Args: []string{"x"}, User: "nobody"}},
		{Op: ir.Op{Kind: ir.OpExec, Args: []string{"x"}, Hosts: []string{"a:1"}}},
		{Op: ir.Op{Kind: ir.OpExec, Args: []string{"x"}, SecretEnv: []string{"TOKEN"}}},
	} {
		if got := operationDigest(t, core.PlatformOf(n, nil)); got == was {
			t.Errorf("a changed operation did not change earthbuild.operation")
		}
	}

	// And a ref reaches it, which is not an Op field at all.
	if got := operationDigest(t, core.PlatformOf(base, []ir.NodeID{{9}})); got == was {
		t.Error("a reference the step reads did not change earthbuild.operation")
	}
}

func operationDigest(t *testing.T, props []core.Property) string {
	t.Helper()

	for _, p := range props {
		if p.Name == "earthbuild.operation" {
			return p.Value
		}
	}

	t.Fatal("no earthbuild.operation property")

	return ""
}
