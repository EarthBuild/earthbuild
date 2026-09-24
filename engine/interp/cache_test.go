package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `CACHE /root/.m2` mounts a directory that outlives the build into every step
// after it.
func TestCacheMountsIntoTheStepsThatFollow(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN before-the-cache
    CACHE /root/.m2
    RUN with-the-cache
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var before, after *ir.Node

	for _, n := range p.Graph.Nodes() {
		switch {
		case strings.Contains(n.Meta.Description, "before-the-cache"):
			before = n
		case strings.Contains(n.Meta.Description, "with-the-cache"):
			after = n
		}
	}

	if before == nil || after == nil {
		t.Fatalf("a step is missing:\n%s", describe(p.Graph.Nodes()))
	}

	if len(before.Op.Mounts) != 0 {
		t.Error("a step before the CACHE line carries the mount")
	}

	if len(after.Op.Mounts) != 1 {
		t.Fatalf("the step after CACHE has %d mounts, want 1", len(after.Op.Mounts))
	}

	if got := after.Op.Mounts[0].Target; got != "/root/.m2" {
		t.Errorf("mounted at %q", got)
	}
}

// A step with a cache mount is not cached.
//
// What it produces may depend on what was in the mount, which no key can bound
// (I3) - so there is no honest key for the result, exactly as for a host step
// (I7). The mount is what makes it fast; the action cache cannot also claim it.
//
// This diverges from the engine that ships, which does cache such steps. It is
// a deliberate choice rather than an omission: a false hit is worse than a slow
// build, and the mount already removes most of the cost.
func TestAStepWithACacheMountIsNotCached(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    CACHE /root/.m2
    RUN build-with-cache
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if !strings.Contains(n.Meta.Description, "build-with-cache") {
			continue
		}

		// **Reversed on 2026-08-19.** This asserted the opposite, on the
		// grounds that a stale layer could serve a step whose output depended on
		// the mount. The same is true of `RUN curl`, which this engine caches -
		// so the rule refused the local directory and permitted the internet,
		// and its effect was that adding `CACHE` to go faster made every rebuild
		// slower (E424).
		//
		// What is asserted instead is the property that makes the reversal safe:
		// the mount's *identity* is in the key, so two steps naming different
		// caches are different steps. Only the contents are undescribed, which is
		// what `CACHE` promises does not change the result.
		if n.Op.NoCache {
			t.Error("a step carrying a cache mount is uncacheable, so adding CACHE" +
				" made this build slower on every rebuild")
		}

		if len(n.Op.Mounts) == 0 {
			t.Error("the step carries no mount, so nothing about the cache is in its key")
		}
	}
}

// Two targets naming one cache get one directory.
//
// A cache that is private per step never warms, which is the opposite of what
// the line asks for.
func TestOneCacheNameIsOneDirectory(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
first:
    FROM alpine:3.22
    CACHE /root/.m2
    RUN one

second:
    FROM alpine:3.22
    CACHE /root/.m2
    RUN two

all:
    BUILD +first
    BUILD +second
`, "all")
	if err != nil {
		t.Fatal(err)
	}

	ids := map[string]bool{}

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			ids[m.ID] = true
		}
	}

	if len(ids) != 1 {
		t.Errorf("two targets naming one cache produced %d directories: %v", len(ids), ids)
	}
}

// `--id` names the cache explicitly, which is what it is for: two different
// paths sharing one store.
func TestCacheIDNamesTheDirectory(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    CACHE --id=shared /root/.m2\n    RUN build\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			if m.ID != "shared" {
				t.Errorf("the cache is called %q, want shared", m.ID)
			}
		}
	}
}
