package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `RUN --mount=type=cache,target=/x` mounts for that step and no other.
//
// The difference from CACHE is the scope, and it is the whole reason both
// exist: CACHE declares something about the rest of the target, while a mount
// on a RUN is about that command. A step that inherited another step's mount
// would see a directory its author never asked for.
func TestRunMountAppliesToThatStepOnly(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN --mount=type=cache,target=/root/.m2 build-with-cache
    RUN plain-step
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		switch {
		case strings.Contains(n.Meta.Description, "build-with-cache"):
			if len(n.Op.Mounts) != 1 {
				t.Fatalf("the mounted step has %d mounts, want 1", len(n.Op.Mounts))
			}

			if got := n.Op.Mounts[0].Target; got != "/root/.m2" {
				t.Errorf("mounted at %q", got)
			}

			// Cacheable since E424: a cache mount is an accelerator, its
			// identity is in the key, and only its contents are undescribed -
			// which is what the construct promises does not change the result.
			if n.Op.NoCache {
				t.Error("a step carrying a cache mount is uncacheable, which is" +
					" what made CACHE slow down every rebuild")
			}

		case strings.Contains(n.Meta.Description, "plain-step"):
			if len(n.Op.Mounts) != 0 {
				t.Error("the next step inherited the mount")
			}
		}
	}
}

// `id=` names the cache, so two steps can share one.
func TestRunMountIDNamesTheCache(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --mount=type=cache,target=/x,id=shared build\n", testMain)
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

// A mount type this engine cannot provide is refused by name.
//
// `type=secret` hands a credential to a step; `type=tmpfs` gives it memory that
// disappears. Neither is a cache, and providing a cache instead would be a step
// running with something other than what it asked for - a secret silently
// absent is the worst of them, because the command that needed it fails
// somewhere far away.
func TestUnsupportedMountTypesAreRefused(t *testing.T) {
	t.Parallel()

	for _, spec := range []string{
		"type=secret,id=token,target=/run/secret",
		"type=tmpfs,target=/tmp/scratch",
		// **`type=bind` is not here any more**: a view of the build context is
		// built (§3.3d), so refusing the type outright would be refusing
		// something this engine does. A path outside the context is still
		// refused - it has no ν and cannot be keyed - but the refusal is about
		// the path rather than the type, and TestABoundViewOutsideTheContextIsRefused
		// is where that belongs.
	} {
		t.Run(spec, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n    RUN --mount="+spec+" build\n", testMain)
			if err == nil {
				t.Fatalf("--mount=%s was accepted", spec)
			}

			kind, _, _ := strings.Cut(spec, ",")
			if !strings.Contains(err.Error(), strings.TrimPrefix(kind, "type=")) {
				t.Errorf("the refusal does not name the type:\n%s", err)
			}
		})
	}
}

// A mount with no target is refused: there is nowhere to put it.
func TestAMountNeedsATarget(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --mount=type=cache build\n", testMain)
	if err == nil {
		t.Fatal("a mount with no target was accepted")
	}

	if !strings.Contains(err.Error(), "target") {
		t.Errorf("the refusal does not say what is missing:\n%s", err)
	}
}

// A view of something outside the build context is refused.
//
// §3.3d: ν is a key or the local context, and a path on the machine running the
// build is neither. It cannot be digested into the graph, so it cannot be keyed
// - and a step reading bytes no key describes is the false hit I3 forbids.
//
// The refusal names the path rather than the mount type, because the type is
// fine and the path is not.
func TestABoundViewOutsideTheContextIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n"+
		"    RUN --mount=type=bind,source=/host,target=/in build\n", testMain)
	if err == nil {
		t.Fatal("a view of a path outside the context was accepted")
	}

	for _, want := range []string{"/host", "build context"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal never mentions %q:\n%s", want, err)
		}
	}

	// The construct that failed, not another one. resolveContext said "COPY"
	// whatever asked, which sent the reader to a line with no COPY on it.
	if strings.Contains(err.Error(), "COPY") {
		t.Errorf("a RUN --mount is reported as a COPY:\n%s", err)
	}
}
