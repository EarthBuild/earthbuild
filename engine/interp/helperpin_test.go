package interp_test

import (
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A helper is pinned by its contents, not by the path it was written as.
//
// **`--helper ./go.wasm` is a name, and a name is not an identity.** The helper
// decides what a unit is, what it is called and what bytes are inside each
// frame, so two machines running different helpers over one cache produce units
// that are not the same units - filed under digests that do not match, and
// importable into each other. Κ₁ hashed the path, which two machines can hold
// identically over different bytes, so that whole argument rested on a string
// nobody had checked.
//
// The same reasoning as Θ one construct over (I17): a mutable reference is
// resolved once, before the key is taken, and what keys is what it resolved to.
func TestAHelperIsPinnedByItsContents(t *testing.T) {
	t.Parallel()

	const src = "VERSION 0.8\nmain:\n    FROM alpine:3.22\n" +
		"    CACHE --id k --portable-except '' --helper ./h.wasm /c\n    RUN echo hi\n"

	one := keyWith(t, src, interp.WithHelperResolver(fixedHelper("aaaa")))
	two := keyWith(t, src, interp.WithHelperResolver(fixedHelper("bbbb")))

	if one == two {
		t.Error("two different helpers at one path key the same step" +
			"\n  so a worker's helper can disagree with the driver's about what a" +
			" unit is, and neither end finds out")
	}
}

// The pin reaches the mount, where the machine that has to run the helper can
// read it.
//
// A key that separates two helpers is worth nothing on its own: the worker is
// not the machine that read `./h.wasm` and has no way to find it. The digest is
// what travels, because it is the one name for a helper that means the same
// thing on both machines.
func TestTheHelperPinReachesTheMount(t *testing.T) {
	t.Parallel()

	p, err := interp.Build("VERSION 0.8\nmain:\n    FROM alpine:3.22\n"+
		"    CACHE --id k --portable-except '' --helper ./h.wasm /c\n    RUN echo hi\n",
		testMain, interp.WithHelperResolver(fixedHelper("cccc")))
	if err != nil {
		t.Fatal(err)
	}

	m, ok := cacheMountOf(p.Graph.Root)
	if !ok {
		t.Fatal("the plan has no cache mount, so this guard is testing nothing")
	}

	if m.Helper != "./h.wasm" {
		t.Errorf("the helper was written as %q and reads as %q"+
			"\n  a build reporting a cache should say what its author wrote", "./h.wasm", m.Helper)
	}

	if m.HelperID != ir.DigestOf([]byte("cccc")).String() {
		t.Errorf("the mount carries helper pin %q, want the digest of the module"+
			"\n  a worker handed this step cannot find the helper it must run", m.HelperID)
	}
}

// Without a resolver the reference is left as written, exactly as an image is.
//
// A plan-only caller - `ls`, `doc`, corpus analysis - must produce a graph
// without touching the filesystem, and an unresolvable helper is a coarser key
// rather than a refused build. What it must not do is claim a pin it does not
// have.
func TestWithoutAResolverTheHelperIsLeftAsWritten(t *testing.T) {
	t.Parallel()

	p, err := interp.Build("VERSION 0.8\nmain:\n    FROM alpine:3.22\n"+
		"    CACHE --id k --portable-except '' --helper ./h.wasm /c\n    RUN echo hi\n", testMain)
	if err != nil {
		t.Fatal(err)
	}

	m, ok := cacheMountOf(p.Graph.Root)
	if !ok {
		t.Fatal("the plan has no cache mount, so this guard is testing nothing")
	}

	if m.HelperID != "" {
		t.Errorf("a build with no helper resolver claims pin %q", m.HelperID)
	}
}

// A helper that cannot be read leaves the build alone.
//
// The position `pin` already takes for a registry that cannot be reached: the
// pinning is worth having and is not worth refusing a build over. A cache that
// does not cross is a slower build elsewhere; a refused step is no build at all.
func TestAHelperThatCannotBeReadDoesNotFailTheBuild(t *testing.T) {
	t.Parallel()

	_, err := interp.Build("VERSION 0.8\nmain:\n    FROM alpine:3.22\n"+
		"    CACHE --id k --portable-except '' --helper ./gone.wasm /c\n    RUN echo hi\n",
		testMain, interp.WithHelperResolver(func(string) (string, error) {
			return "", errors.New("no such file")
		}))
	if err != nil {
		t.Fatalf("an unreadable helper failed the build: %v", err)
	}
}

// fixedHelper is a resolver answering with one module's digest, whatever it is
// asked.
func fixedHelper(body string) interp.ResolveHelper {
	return func(string) (string, error) { return ir.DigestOf([]byte(body)).String(), nil }
}

// keyWith is plan's sibling for the cases that need an option.
func keyWith(t *testing.T, src string, opts ...interp.Option) ir.NodeID {
	t.Helper()

	p, err := interp.Build(src, testMain, opts...)
	if err != nil {
		t.Fatal(err)
	}

	return p.Graph.Root.ID()
}

// cacheMountOf finds the one named cache mount under a node.
func cacheMountOf(n *ir.Node) (ir.Mount, bool) {
	for _, m := range n.Op.Mounts {
		if m.ID != "" {
			return m, true
		}
	}

	for _, in := range n.Inputs {
		if m, ok := cacheMountOf(in); ok {
			return m, true
		}
	}

	return ir.Mount{}, false
}
