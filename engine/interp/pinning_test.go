package interp_test

import (
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// resolver is a registry that answers what a reference names, and counts.
type resolver struct {
	mu    sync.Mutex
	calls []string
	give  map[string]string
}

func (r *resolver) resolve(ref, platform string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, ref+" "+platform)

	if to, ok := r.give[ref]; ok {
		return to, nil
	}

	return ref + "@sha256:" + "0000000000000000000000000000000000000000000000000000000000000000", nil
}

func imageNodes(g interface{ Nodes() []*ir.Node }) []*ir.Node {
	var out []*ir.Node

	for _, n := range g.Nodes() {
		if n.Op.Kind == ir.OpImage {
			out = append(out, n)
		}
	}

	return out
}

// A reference resolves once per build, however many targets name it.
//
// I17: within one build a mutable reference resolves exactly once, and every key
// depending on it sees the same digest. Resolving per use would let a tag that
// moved mid-build put two different bases in one build - two machines' worth of
// divergence from one Earthfile.
func TestAReferenceResolvesOncePerBuild(t *testing.T) {
	t.Parallel()

	r := &resolver{give: map[string]string{
		"alpine:3.22": "alpine@sha256:aaaa000000000000000000000000000000000000000000000000000000000000",
	}}

	p, err := interp.Build(versioned+`
all:
    BUILD +a
    BUILD +b
    BUILD +c

a:
    FROM alpine:3.22
    RUN one

b:
    FROM alpine:3.22
    RUN two

c:
    FROM alpine:3.22
    RUN three
`, "all", interp.WithImageResolver(r.resolve))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.calls) != 1 {
		t.Errorf("resolved %d time(s) for one reference used three times: %v"+
			"\n  a tag that moved between calls would put two bases in one build", len(r.calls), r.calls)
	}

	for _, n := range imageNodes(p.Graph) {
		if got := n.Op.Args[0]; got != "alpine@sha256:aaaa000000000000000000000000000000000000000000000000000000000000" {
			t.Errorf("an image node still names %q, not what it resolved to", got)
		}
	}
}

// What a reference resolved to reaches the key.
//
// I3: if anything the step could observe differs, the key differs. A key derived
// from the reference is stable while the thing it names moves, which is a false
// hit - the one failure that must never occur. Node identity hashes Op.Args, so
// pinning the digest into the args is what closes it.
func TestAMovedTagIsADifferentBuild(t *testing.T) {
	t.Parallel()

	src := versioned + `
main:
    FROM alpine:latest
    RUN build
`

	ids := make([]ir.NodeID, 0, 2)

	for _, to := range []string{
		"alpine@sha256:1111000000000000000000000000000000000000000000000000000000000000",
		"alpine@sha256:2222000000000000000000000000000000000000000000000000000000000000",
	} {
		r := &resolver{give: map[string]string{"alpine:latest": to}}

		p, err := interp.Build(src, "main", interp.WithImageResolver(r.resolve))
		if err != nil {
			t.Fatal(err)
		}

		var run *ir.Node

		for _, n := range p.Graph.Nodes() {
			if n.Op.Kind == ir.OpExec {
				run = n
			}
		}

		if run == nil {
			t.Fatal("no step to key")
		}

		ids = append(ids, run.ID())
	}

	if ids[0] == ids[1] {
		t.Errorf("latest moved and the step kept key %v"+
			"\n  every later build sharing that key would hit a result built on the other image", ids[0])
	}
}

// Without a resolver a reference is left as written, and nothing pretends it was
// pinned.
//
// Unlike the other capability seams, an absent resolver does not refuse the
// construct: FROM is in every Earthfile, and a plan-only caller - `ls`, `doc`,
// corpus analysis - must still be able to produce a graph without reaching the
// network. What it must not do is look pinned.
func TestWithoutAResolverAReferenceIsLeftAsWritten(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN build
`, "main")
	if err != nil {
		t.Fatal(err)
	}

	got := imageNodes(p.Graph)
	if len(got) != 1 {
		t.Fatalf("%d image node(s), want 1", len(got))
	}

	if got[0].Op.Args[0] != "alpine:3.22" {
		t.Errorf("the reference became %q with nothing to resolve it", got[0].Op.Args[0])
	}

	if len(p.Pinned) != 0 {
		t.Errorf("nothing resolved and the build reports pinning %v", p.Pinned)
	}
}

// What resolved is recorded, so a build can say which image it used.
//
// Provenance (B.3): comparing two builds' pinnings is how a moved tag is told
// from a changed Earthfile.
func TestWhatResolvedIsRecorded(t *testing.T) {
	t.Parallel()

	to := "alpine@sha256:3333000000000000000000000000000000000000000000000000000000000000"
	r := &resolver{give: map[string]string{"alpine:3.22": to}}

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN build
`, "main", interp.WithImageResolver(r.resolve))
	if err != nil {
		t.Fatal(err)
	}

	if p.Pinned["alpine:3.22"] != to {
		t.Errorf("the build recorded %v, want alpine:3.22 -> %s", p.Pinned, to)
	}
}
