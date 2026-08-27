package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A condition's exit status is its answer, and the mapping is the whole of the
// semantics: zero is true, anything else is false, and a build that could not
// run it at all is neither.
//
// The third case is the one worth having a test for. A sandbox that failed to
// start looks like a condition that said no, and answering false there takes a
// branch the Earthfile did not select while reporting success.
func TestAnExitStatusBecomesABranch(t *testing.T) {
	t.Parallel()

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine"}}}

	for _, tc := range []struct {
		name    string
		err     error
		want    bool
		wantErr string
	}{
		{name: "exit zero is true", err: nil, want: true},
		{
			name: "a non-zero exit is false",
			// A failed step carries its own output, which is where the
			// message worth reading usually is.
			err: &core.StepError{Source: "Earthfile:4", Exit: 1, Output: "not found\n"},
		},
		{
			name:    "a build that could not run is an error",
			err:     errors.New("the sandbox would not start"),
			wantErr: "the sandbox would not start",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var probe *ir.Node

			run := func(_ context.Context, g *ir.Graph) (string, error) {
				probe = g.Root

				return "what it printed\n", tc.err
			}

			res, err := decideByRunning(context.Background(), run,
				[]string{testCommand, "-v", testUnbuffer}, base, "", "Earthfile:4")

			switch {
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error is %v, want one mentioning %q", err, tc.wantErr)
				}

				return
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			}

			if got := res.Exit == 0; got != tc.want {
				t.Errorf("the branch is %v, want %v", got, tc.want)
			}

			// A substitution reads the output through the same seam, so it
			// has to survive whichever way the command went.
			if res.Output == "" {
				t.Error("the output was dropped")
			}

			// The condition runs through a shell, on the recipe's own
			// filesystem: `command -v x` means what the shell means by it, and
			// it is asked of the image built so far rather than a bare one.
			if probe == nil {
				t.Fatal("nothing was run")
			}

			if len(probe.Inputs) != 1 || probe.Inputs[0] != base {
				t.Error("the condition did not run on the step before it")
			}

			if got := strings.Join(probe.Op.Args, " "); !strings.Contains(got, "command -v unbuffer") {
				t.Errorf("ran %q, want the condition as written", got)
			}
		})
	}
}

// The probe inherits the working directory and environment of the step it
// follows.
//
// `IF [ -f config ]` after a WORKDIR asks about that directory. Running it
// somewhere else answers a different question and looks exactly like a correct
// answer - the failure this whole seam is arranged to make visible.
func TestTheProbeInheritsTheStepsContext(t *testing.T) {
	t.Parallel()

	base := &ir.Node{Op: ir.Op{
		Kind: ir.OpExec,
		Args: []string{"prepare"},
		Dir:  "/work/sub",
		Env:  map[string]string{"MODE": "release"},
	}}

	var probe *ir.Node

	run := func(_ context.Context, g *ir.Graph) (string, error) {
		probe = g.Root

		return "", nil
	}

	// The working directory comes from the interpreter rather than from the
	// step: WORKDIR changes the state without producing a step, so the last
	// step's Dir is whatever it happened to be and not where the build now is.
	// `WORKDIR /var/app` then `SAVE IMAGE app:$(cat version)` reads a file the
	// line above put in /var/app, and the last step may have run anywhere.
	_, err := decideByRunning(context.Background(), run,
		[]string{"[", "-f", "config", "]"}, base, "/work/sub", "Earthfile:9")
	if err != nil {
		t.Fatal(err)
	}

	if probe.Op.Dir != "/work/sub" {
		t.Errorf("the condition runs in %q, want where the build is", probe.Op.Dir)
	}

	if probe.Op.Env["MODE"] != "release" {
		t.Errorf("the condition's environment is %v, want the step's", probe.Op.Env)
	}
}

// A condition on a LOCALLY target is refused rather than run.
//
// Deciding it needs the target's earlier steps to have run, and a host step is
// attempted exactly once (I7), so running them to decide would run them a
// second time in the build proper. `RUN rm -rf build` twice is not a cost, it
// is a defect.
func TestALocalConditionIsRefusedWithItsReason(t *testing.T) {
	t.Parallel()

	host := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"prepare"}}}

	_, err := decideByRunning(context.Background(),
		func(context.Context, *ir.Graph) (string, error) { return "", nil },
		[]string{"[", "-f", "flag.txt", "]"}, host, "", "Earthfile:5")
	if err == nil {
		t.Fatal("a condition on a LOCALLY target was evaluated in a sandbox")
	}

	for _, want := range []string{"LOCALLY", "twice"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// The local worker declares the platform it can actually run.
//
// Without this it declared none, and the scheduler's platform affinity refuses
// every node that names one - so `FROM --platform=linux/arm64 alpine` planned
// correctly and then failed with "no eligible worker" on a machine that runs
// exactly that. The affinity rule is right; the worker was lying about itself.
func TestTheLocalWorkerDeclaresItsPlatform(t *testing.T) {
	t.Parallel()

	w := localWorker("linux/arm64")

	if w.Platform.OS != "linux" || w.Platform.Arch != "arm64" {
		t.Errorf("the worker declares %+v, want linux/arm64", w.Platform)
	}

	if !w.IsInvoker {
		t.Error("the local worker is not marked as the invoker, so no LOCALLY step may run")
	}

	// An unset platform falls back to this machine's, rather than to none:
	// declaring nothing means nothing can be scheduled onto it.
	if got := localWorker("").Platform; got == (ir.Platform{}) {
		t.Error("with no platform given the worker declares none, so nothing with a platform can run")
	}
}

// The image an Earthfile declares is turned into a spec for the writer.
//
// Kept apart from the writing so the mapping can be checked without a layer
// store: what a `SAVE IMAGE` said - entrypoint, environment, labels, working
// directory - has to arrive in the config a runtime reads, and an environment
// held as a map has to come out as the `K=V` list the format uses.
func TestAnImageSpecCarriesWhatTheEarthfileDeclared(t *testing.T) {
	t.Parallel()

	spec := specFor(interp.Image{
		Ref: "app:latest",
		Config: interp.Config{
			Entrypoint: []string{"/app/main"},
			Cmd:        []string{"--serve"},
			WorkingDir: "/app",
			User:       "nobody",
			Env:        map[string]string{"PATH": "/usr/bin", "LANG": "C"},
			Labels:     map[string]string{"org.example.by": "earthbuild"},
			Exposed:    []string{"8080/tcp"},
		},
	}, "linux/arm64", nil)

	if spec.Ref != "app:latest" {
		t.Errorf("the spec is called %q", spec.Ref)
	}

	if spec.Platform.OS != "linux" || spec.Platform.Architecture != "arm64" {
		t.Errorf("the spec says %+v, want linux/arm64", spec.Platform)
	}

	if got := strings.Join(spec.Config.Entrypoint, " "); got != "/app/main" {
		t.Errorf("entrypoint is %q", got)
	}

	if spec.Config.WorkingDir != "/app" || spec.Config.User != "nobody" {
		t.Errorf("working directory or user was lost: %+v", spec.Config)
	}

	// Sorted, because a map has no order and an image's identity is the digest
	// of this config: an unordered environment is a different image every run.
	if got := strings.Join(spec.Config.Env, ","); got != "LANG=C,PATH=/usr/bin" {
		t.Errorf("the environment is %q, want it sorted", got)
	}

	if _, ok := spec.Config.ExposedPorts["8080/tcp"]; !ok {
		t.Errorf("the exposed port was lost: %+v", spec.Config.ExposedPorts)
	}

	if spec.Config.Labels["org.example.by"] != "earthbuild" {
		t.Errorf("the label was lost: %+v", spec.Config.Labels)
	}
}

// A build records which way each condition went, for the next build to use.
//
// The site is where the condition is written and what it says, so the history
// survives an edit to anything before it - which is most commits, and exactly
// when a developer is iterating.
func TestAConditionsOutcomeIsRecorded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	learned := core.NewPredictions()

	recordBranch(learned, []string{testCommand, "-v", testUnbuffer}, "Earthfile:12", true, "")
	recordBranch(learned, []string{testCommand, "-v", testUnbuffer}, "Earthfile:12", true, "")
	recordBranch(learned, []string{testCommand, "-v", testUnbuffer}, "Earthfile:12", true, "")

	err := savePredictions(dir, learned)
	if err != nil {
		t.Fatal(err)
	}

	next, err := loadPredictions(dir)
	if err != nil {
		t.Fatal(err)
	}

	branch, confident := next.Predict(siteOf([]string{testCommand, "-v", testUnbuffer}, "Earthfile:12", ""))
	if !confident || !branch {
		t.Errorf("the next build does not know which way this condition goes (%v, %v)", branch, confident)
	}

	// A different line is a different site, even with the same words.
	if _, confident := next.Predict(siteOf([]string{testCommand, "-v", testUnbuffer}, "Earthfile:99", "")); confident {
		t.Error("history from one line was applied to another")
	}
}
