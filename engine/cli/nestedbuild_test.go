package cli

import (
	"path/filepath"
	"testing"
)

// A target built so that a plan can be made is not the caller's build.
//
// **What this cost.** `--helper ../../..+cache-helper/build/h.wasm` and `FROM
// DOCKERFILE +gen/` name an artifact the way `COPY` does, and a `COPY` does not
// run the named target's `AS LOCAL` exports. Built as an ordinary invocation it
// did - and the destination is relative to the directory the *caller* started
// in, not to the Earthfile the target came from. So planning
// `examples/cache-helpers/go-build+compile` ran the repository root's
//
//	SAVE ARTIFACT go.mod AS LOCAL go.mod
//
// and wrote the engine's own go.mod over the example's, plus four .wasm files
// and a go.sum that did not belong there. Observed, not imagined.
func TestATargetBuiltToMakeAPlanWritesNothingLocal(t *testing.T) {
	t.Parallel()

	o := Options{Dir: "examples/cache-helpers/go-build", Target: "compile"}

	got := nested(o, filepath.Join("examples", "cache-helpers", "go-build", "..", "..", ".."))

	if !got.NoOutput {
		t.Error("the nested build writes AS LOCAL artifacts into the caller's tree;" +
			"\n  it is building a target to read one file out of, not running the caller's build")
	}

	// The directory is the one the reference resolved to, so what the nested
	// build does read - its context, its own relative references - is its own.
	if want := filepath.Clean("."); filepath.Clean(got.Dir) != want {
		t.Errorf("the nested build runs in %q, want %q", got.Dir, want)
	}

	if o.NoOutput || o.Dir != "examples/cache-helpers/go-build" {
		t.Error("the caller's own options were modified")
	}
}

// A target reference is cut where COPY cuts it: at the first separator after
// the `+`.
//
// **Cut at the last one**, `+cache-helper/build/h.wasm` asked for a target
// called `+cache-helper/build`, which nothing is called. It worked for a
// one-segment artifact and failed for every deeper one - as a cache that
// silently does not share (I11), because a helper that cannot be got leaves the
// mount unpinned rather than failing the build:
//
//	cache eg-go-build: not shared: read the helper
//	../../..+cache-helper/build/cachehelper-go-build.wasm: no such file
func TestATargetReferenceIsCutWhereCopyCutsIt(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ ref, target, name string }{
		{"+gen/", "+gen", ""},
		{"+gen", "+gen", ""},
		{"+gen/other.Dockerfile", "+gen", "other.Dockerfile"},
		{"/p+cache-helper/build/h.wasm", "/p+cache-helper", "build/h.wasm"},
		{"/p+cache-helper/", "/p+cache-helper", ""},
	} {
		target, name := targetAndArtifact(c.ref)
		if target != c.target || name != c.name {
			t.Errorf("targetAndArtifact(%q) = %q, %q\n  want %q, %q",
				c.ref, target, name, c.target, c.name)
		}
	}
}
