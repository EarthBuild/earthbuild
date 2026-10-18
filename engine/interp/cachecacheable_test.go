package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step with a CACHE mount is cacheable, because a cache is an accelerator.
//
// **This reverses the original rule and the reason is an inconsistency in it.**
// A cache mount was treated as making the step uncacheable, on the grounds that
// what it produces may depend on the mount's contents, which no key describes.
// True - and equally true of `RUN curl https://…`, which this engine caches
// without hesitation. It refused the local directory and permitted the internet.
//
// What `CACHE` offers is that a cold cache gives the same result, slower. A step
// that needs the cache's contents to be *correct* is relying on something the
// construct never promised, and the same reliance across builds is already
// unbounded today.
//
// The practical cost of the old rule was the opposite of what the construct is
// for: adding `CACHE` to make a step faster made every rebuild slower, because
// the step could never be served from the action cache again (E424).
func TestACacheMountDoesNotMakeAStepUncacheable(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    CACHE /root/.m2
    RUN mvn package
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	var seen int

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec || len(n.Op.Mounts) == 0 {
			continue
		}

		seen++

		if n.Op.NoCache {
			t.Errorf("a step with a cache mount is uncacheable, so adding CACHE"+
				" made this build slower on every rebuild (%s)", n.Meta.Source)
		}
	}

	if seen == 0 {
		t.Fatal("no step carries the cache mount")
	}
}

// A persisted cache mount does make the step uncacheable, and that one is real.
//
// `--persist` copies the mount's contents *into* the image, so what the step
// produces genuinely includes them - the mount is an input to the output rather
// than an accelerator beside it, and no key over this step's inputs describes
// what was in the directory.
func TestAPersistedCacheMountKeepsTheStepUncacheable(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    CACHE --persist /out
    RUN make
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	var seen int

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec || len(n.Op.Mounts) == 0 {
			continue
		}

		seen++

		if !n.Op.NoCache {
			t.Errorf("a step whose cache is copied into its image is cacheable,"+
				" and no key describes what was in the cache (%s)", n.Meta.Source)
		}
	}

	if seen == 0 {
		t.Fatal("no step carries the cache mount")
	}
}
