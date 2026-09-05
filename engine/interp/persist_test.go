package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `CACHE --persist /path` keeps the cache *and* puts its contents in the image.
//
// The difference from a plain CACHE is which side of the layer the contents end
// up on, and it is not a detail: a plain cache mount is bound over the step's
// filesystem so what goes in it is excluded from the layer by construction.
// `--persist` asks for the opposite, so it cannot be a bind at all - the
// contents have to be written into the step's own root to be captured.
func TestPersistIsRecordedOnTheMount(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    CACHE --persist /state
    RUN build
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var seen bool

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			seen = true

			if !m.Persist {
				t.Error("--persist was not recorded, so the contents would be excluded from the image")
			}

			if m.Target != "/state" {
				t.Errorf("mounted at %q", m.Target)
			}
		}
	}

	if !seen {
		t.Fatalf("no mount reached the graph:\n%s", describe(p.Graph.Nodes()))
	}
}

// Persisting and not persisting are different steps.
//
// They produce different images from the same command - one carries the cache's
// contents and one does not - so keying them alike would let a build hit an
// entry for the other and ship the wrong image.
func TestPersistChangesTheKey(t *testing.T) {
	t.Parallel()

	mk := func(flag string) string {
		p, err := interp.Build(versioned+
			"\nmain:\n    FROM alpine:3.22\n    CACHE "+flag+"/state\n    RUN build\n", testMain)
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root.ID().String()
	}

	if mk("--persist ") == mk("") {
		t.Error("a persisted cache and an ordinary one share an identity")
	}
}

// A plain CACHE still keeps its contents out of the image.
func TestAPlainCacheDoesNotPersist(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    CACHE /state\n    RUN build\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			if m.Persist {
				t.Error("an ordinary CACHE was marked as persisting")
			}
		}
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "build") {
		t.Errorf("the step is missing:\n%s", got)
	}
}
