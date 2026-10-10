package interp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A helper may be something this build produces.
//
// **What makes a helper an ordinary build input.** `--helper ./h.wasm` is a file
// somebody has to have built already, which is why the examples need two
// commands and cannot join the `examples-N` CI targets: a `BUILD` is one
// invocation and the helper must exist before it is planned.
//
// `+target/artifact` closes that. The reference is resolved the way `COPY`
// resolves one - the target is built while planning, exactly as `FROM
// DOCKERFILE` builds the target that writes its Dockerfile - and what comes back
// is a file on disk, which is what the resolver wanted all along.
func TestAHelperCanBeAnArtifactOfThisBuild(t *testing.T) {
	t.Parallel()

	made := t.TempDir()
	module := []byte("a module some target produced")

	if err := os.WriteFile(filepath.Join(made, "h.wasm"), module, 0o600); err != nil {
		t.Fatal(err)
	}

	var asked []string

	p, err := interp.Build(`VERSION 0.8
main:
    FROM alpine:3.22
    CACHE --id k --portable-except '' --helper +gen/h.wasm /c
    RUN echo hi
`, testMain,
		interp.WithArtifacts(func(ref, _ string) (string, error) {
			asked = append(asked, ref)

			return made, nil
		}),
		interp.WithHelperResolver(func(ref, dir string) (string, error) {
			at := ref
			if !filepath.IsAbs(at) {
				at = filepath.Join(dir, ref)
			}

			b, readErr := os.ReadFile(at) //nolint:gosec // a test fixture
			if readErr != nil {
				return "", readErr
			}

			return ir.DigestOf(b).String(), nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	// The whole reference. The builder stages the one artifact that was asked
	// for, under the name it was asked for, so the reader finds it where it
	// asked - which a whole-output request cannot offer, because an artifact's
	// recorded path is absolute inside the step and the reference is relative
	// to that step's working directory.
	if len(asked) != 1 || asked[0] != "+gen/h.wasm" {
		t.Fatalf("the builder was asked for %v, want [+gen/h.wasm]", asked)
	}

	m, ok := cacheMountOf(p.Graph.Root)
	if !ok {
		t.Fatal("the plan has no cache mount")
	}

	if m.Helper != "+gen/h.wasm" {
		t.Errorf("the mount reads the helper as %q, want it as the author wrote it", m.Helper)
	}

	if want := ir.DigestOf(module).String(); m.HelperID != want {
		t.Errorf("pinned %q, want the digest of what the target produced (%s)", m.HelperID, want)
	}
}

// Without anywhere to build it, the cache simply does not cross.
//
// **Degrade, not refuse**, which is every other `--helper` failure and the
// reason: a plan-only caller - `ls`, `doc`, the corpus sweep - must produce a
// graph without building anything, and a cache that does not cross is a slower
// build somewhere else where a refused step is no build at all (I11).
func TestAnArtifactHelperWithNowhereToBuildItIsNotPinned(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8
main:
    FROM alpine:3.22
    CACHE --id k --portable-except '' --helper +gen/h.wasm /c
    RUN echo hi
`, testMain, interp.WithHelperResolver(fixedHelper("never asked")))
	if err != nil {
		t.Fatalf("an artifact helper with nowhere to build it failed the plan: %v", err)
	}

	m, ok := cacheMountOf(p.Graph.Root)
	if !ok {
		t.Fatal("the plan has no cache mount")
	}

	if m.HelperID != "" {
		t.Errorf("claimed pin %q for a module nothing built", m.HelperID)
	}
}

// A target that cannot be built leaves the cache unshared rather than failing.
func TestAnArtifactHelperThatWillNotBuildIsNotPinned(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8
main:
    FROM alpine:3.22
    CACHE --id k --portable-except '' --helper +gen/h.wasm /c
    RUN echo hi
`, testMain,
		interp.WithArtifacts(func(string, string) (string, error) {
			return "", os.ErrNotExist
		}),
		interp.WithHelperResolver(fixedHelper("never asked")))
	if err != nil {
		t.Fatalf("a helper target that would not build failed the plan: %v", err)
	}

	m, _ := cacheMountOf(p.Graph.Root)
	if m.HelperID != "" {
		t.Errorf("claimed pin %q after the build failed", m.HelperID)
	}
}

// Two helpers from one target are built once.
//
// The memo is the point: `FROM DOCKERFILE` builds its target once per plan and
// this must too, or an Earthfile with a cache mount in forty steps builds the
// helper forty times.
func TestAnArtifactHelperIsBuiltOncePerPlan(t *testing.T) {
	t.Parallel()

	made := t.TempDir()
	if err := os.WriteFile(filepath.Join(made, "h.wasm"), []byte("m"), 0o600); err != nil {
		t.Fatal(err)
	}

	built := 0

	_, err := interp.Build(`VERSION 0.8
main:
    FROM alpine:3.22
    CACHE --id a --portable-except '' --helper +gen/h.wasm /a
    RUN echo one
    CACHE --id b --portable-except '' --helper +gen/h.wasm /b
    RUN echo two
`, testMain,
		interp.WithArtifacts(func(string, string) (string, error) {
			built++

			return made, nil
		}),
		interp.WithHelperResolver(fixedHelper("m")))
	if err != nil {
		t.Fatal(err)
	}

	if built != 1 {
		t.Errorf("the helper's target was built %d times, want once per plan", built)
	}
}

// An artifact several directories down its own output is found.
//
// **The case a real build caught and the first test did not.** `+cache-helper`
// saves `build/cachehelper-npm.wasm`, and handing the whole reference to the
// builder cut it at the *last* `/` - asking for a target called
// `+cache-helper/build`, which does not exist. It worked for a one-segment
// artifact and failed for every deeper one, silently, as a cache that simply
// did not share.
func TestAHelperDeepInATargetsOutputIsFound(t *testing.T) {
	t.Parallel()

	made := t.TempDir()
	module := []byte("a module several directories down")

	if err := os.MkdirAll(filepath.Join(made, "build"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(made, "build", "h.wasm"), module, 0o600); err != nil {
		t.Fatal(err)
	}

	var asked []string

	p, err := interp.Build(`VERSION 0.8
main:
    FROM alpine:3.22
    CACHE --id k --portable-except '' --helper +gen/build/h.wasm /c
    RUN echo hi
`, testMain,
		interp.WithArtifacts(func(ref, _ string) (string, error) {
			asked = append(asked, ref)

			return made, nil
		}),
		interp.WithHelperResolver(func(ref, dir string) (string, error) {
			at := ref
			if !filepath.IsAbs(at) {
				at = filepath.Join(dir, ref)
			}

			b, readErr := os.ReadFile(at) //nolint:gosec // a test fixture
			if readErr != nil {
				return "", readErr
			}

			return ir.DigestOf(b).String(), nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	if len(asked) != 1 || asked[0] != "+gen/build/h.wasm" {
		t.Fatalf("the builder was asked for %v, want [+gen/build/h.wasm]"+
			"\n  the target is cut at the first slash after the `+`, and the rest"+
			" is the path within the output - which the builder needs, because"+
			" it stages that one artifact under that name", asked)
	}

	m, _ := cacheMountOf(p.Graph.Root)
	if want := ir.DigestOf(module).String(); m.HelperID != want {
		t.Errorf("pinned %q, want %s - the artifact is two directories down and"+
			" must still be found", m.HelperID, want)
	}
}
