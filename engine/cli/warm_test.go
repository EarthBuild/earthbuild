package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/interp"
)

const localOnly = `VERSION 0.8
main:
    LOCALLY
    RUN echo hello
`

const needsOne = `VERSION 0.8
main:
    FROM alpine:3.22
    RUN echo hello
`

// markerExecutor is an identity, not a working executor: these tests ask which
// executor `executorFor` chose, never what it does.
var markerExecutor exec.Executor

// withSandbox stands in for a sandbox that has already been built.
//
// The Once is burnt first, because `sandboxed` is where a real one would be
// constructed and every path that reaches for the sandbox now goes through it -
// which is the point: reading the field directly is what raced with the
// goroutine that fills it.
func withSandbox(t *testing.T, used bool) *engine {
	t.Helper()

	g := &engine{o: Options{Dir: t.TempDir()}}

	g.once.Do(func() {})
	g.ex = &markerExecutor
	g.started, g.used = true, used

	return g
}

func planOf(t *testing.T, src string) *interp.Plan {
	t.Helper()

	p, err := interp.Build(src, testMainTarget, interp.WithContext(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}

	return p
}

// A plan that needs no sandbox runs on the host even when a sandbox is already
// there.
//
// `executorFor` used to read "a sandbox exists" as "a probe needed one", which
// held only because the sole way one came to exist was a probe. Starting one
// early breaks that implication, and the two must be told apart before it is:
// a build of nothing but LOCALLY steps that silently switched executors because
// something warmed a VM in the background would be a hint changing a result,
// which is exactly what a hint may not do (I5).
func TestAWarmSandboxDoesNotCaptureAHostOnlyBuild(t *testing.T) {
	t.Parallel()

	// A warmed sandbox: present, but nothing has used it.
	g := withSandbox(t, false)

	e, err := g.executorFor(planOf(t, localOnly))
	if err != nil {
		t.Fatal(err)
	}

	if e == &markerExecutor {
		t.Error("a host-only build was given the sandbox because one happened to be warm")
	}
}

// A sandbox that was actually used to decide a condition is still reused.
//
// Not thrift: a second sandbox has its own layer store, so every step already
// run to answer the condition would be a cache miss in the build that follows.
func TestAUsedSandboxIsStillReused(t *testing.T) {
	t.Parallel()

	g := withSandbox(t, true)

	e, err := g.executorFor(planOf(t, localOnly))
	if err != nil {
		t.Fatal(err)
	}

	if e != &markerExecutor {
		t.Error("the sandbox that answered a condition was thrown away")
	}
}

// A build that needs a sandbox gets the warm one rather than a second.
func TestABuildThatNeedsASandboxTakesTheWarmOne(t *testing.T) {
	t.Parallel()

	g := withSandbox(t, false)

	e, err := g.executorFor(planOf(t, needsOne))
	if err != nil {
		t.Fatal(err)
	}

	if e != &markerExecutor {
		t.Error("a second sandbox was built beside the warm one")
	}
}

// Warming does not wait to be told the project will need a machine.
//
// It used to: a sandbox was started early only for a project whose history
// showed a condition, on the ground that a speculative boot is wasted on a build
// that runs nothing. Measured, that gate was giving up most of the benefit -
// a project with no `IF` but plenty of `RUN` never warmed, and a build needing a
// boot paid 3.92s against 2.83s warmed (E537).
//
// The cost it guarded against did not appear, because nothing waits for the
// boot: a build that needs no machine finishes and exits while it is still in
// flight, and the machine it leaves is the one the next build wants. A true
// no-op - every step a hit and nothing exported - measured 0.64s warmed against
// 0.71s cold.
//
// This test is the record of that decision, so a future gate has to argue with a
// number rather than with a comment.
func TestWarmingIsNotGatedOnHistory(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("cli.go")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(src), "shouldWarm(") {
		t.Error("warming is gated again; if that is right, this test wants the measurement that says so")
	}

	if !strings.Contains(string(src), "g.warm(ctx)") {
		t.Error("nothing starts the sandbox beside interpretation any more")
	}
}

// Every path to the sandbox goes through sandboxed(), including the one that
// only wants to shut it down.
//
// The field it fills is written by whichever goroutine runs the sync.Once, and
// a warm-up runs that Once in the background. A reader that touches the field
// without going through the Once is racing it - and the race detector cannot
// see it without a real VM to boot, so it is asserted structurally instead.
func TestNothingReadsTheSandboxFieldOutsideTheOnce(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("conditions.go")
	if err != nil {
		t.Fatal(err)
	}

	for i, line := range strings.Split(string(src), "\n") {
		code, _, _ := strings.Cut(line, "//")
		if !strings.Contains(code, "g.ex") {
			continue
		}

		// The assignment inside the Once, and the accessor that joins it.
		if strings.Contains(code, "g.ex = e") || strings.Contains(code, "return g.ex") {
			continue
		}

		t.Errorf("conditions.go:%d reads the sandbox outside sandboxed(): %s",
			i+1, strings.TrimSpace(line))
	}
}
