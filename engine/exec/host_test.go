package exec_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func hostStep(t *testing.T, argv ...string) *ir.Node {
	t.Helper()

	return &ir.Node{
		Op:   ir.Op{Kind: ir.OpHost, Args: argv},
		Meta: ir.Meta{Source: "Earthfile:2", Description: "RUN " + strings.Join(argv, " ")},
	}
}

// A host step runs on this machine, which is the whole point of LOCALLY: it can
// see what the machine has and the sandbox does not.
func TestHostStepsRunOnThisMachine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	marker := filepath.Join(dir, "only-here")
	err := os.WriteFile(marker, []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	e, err := exec.New(&countingSandbox{store: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	e.Context = dir

	res, err := e.Run(context.Background(),
		hostStep(t, found(t, "test"), "-f", "only-here"), core.Worker{ID: testLocal}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The test ran in the project directory, so a relative path resolved.
	if res.Exit != 0 {
		t.Errorf("the step exited %d; it did not run in the project directory", res.Exit)
	}
}

// Its result is never captured, however well it went.
//
// Nothing bounds what a host step observed, so there is nothing to cache. The
// scheduler enforces this too; the executor says it as well, because a component
// that reports a truth it knows is better than one relying on another to notice.
func TestHostResultsAreNeverCaptured(t *testing.T) {
	t.Parallel()

	e, err := exec.New(&countingSandbox{store: t.TempDir(), confines: true})
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	e.Context = t.TempDir()

	res, err := e.Run(context.Background(),
		hostStep(t, found(t, "true")), core.Worker{ID: testLocal}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Captured {
		t.Error("a host step produced a cacheable result")
	}
}

// A host step that fails is a result, and its output is what the error is made
// of.
func TestFailingHostStepsReturnTheirOutput(t *testing.T) {
	t.Parallel()

	e, err := exec.New(&countingSandbox{store: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	e.Context = t.TempDir()

	res, err := e.Run(context.Background(),
		hostStep(t, found(t, "sh"), "-c", "echo to-stderr >&2; exit 4"),
		core.Worker{ID: testLocal}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if res.Exit != 4 {
		t.Errorf("exit code is %d, want 4", res.Exit)
	}

	if !strings.Contains(res.Output, "to-stderr") {
		t.Errorf("the output is missing what the step said: %q", res.Output)
	}
}

// A host step inherits the machine's environment, and ε is layered on top.
//
// The test that stood here asserted the opposite, on the reasoning that applies
// to a sandboxed step: ε must bound what the step observed, or its key is a
// claim about something that read more. That reasoning does not reach here. A
// host step is unsandboxed, so nothing bounds it, so it is never cached - there
// is no key to keep sound, and an empty environment leaves no PATH, which means
// a LOCALLY target cannot run anything that is not a shell builtin.
func TestHostStepsInheritTheMachinesEnvironment(t *testing.T) {
	t.Setenv("EARTH_TEST_AMBIENT", "visible")

	e, err := exec.New(&countingSandbox{store: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	e.Context = t.TempDir()

	n := hostStep(t, found(t, "sh"), "-c", "echo [$EARTH_TEST_AMBIENT][$DECLARED]")
	n.Op.Env = map[string]string{"DECLARED": "yes"}

	res, err := e.Run(context.Background(), n, core.Worker{ID: testLocal}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(res.Output, "[visible]") {
		t.Errorf("the machine's environment did not reach the step: %q", res.Output)
	}

	if !strings.Contains(res.Output, "[yes]") {
		t.Errorf("the declared environment did not reach the step: %q", res.Output)
	}
}
