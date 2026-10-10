package interp_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `COPY *.sh /dst` copies what the pattern matches.
//
// A pattern is ordinary Earthfile syntax and the corpus is full of it - fifty
// COPY lines here use one. It was refused with "is not in the build context",
// which is both a refusal of valid input and a misleading account of it: the
// files are there, and it is the `*` that could not be stat'd.
func TestCopyExpandsAPattern(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{
		"scripts/one.sh":   "one\n",
		"scripts/two.sh":   "two\n",
		"scripts/notes.md": "not a script\n",
	})

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    COPY scripts/*.sh /dst/\n",
		testMain, interp.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}

	var got []string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpLocal {
			got = append(got, n.Op.Args[0])
		}
	}

	// Compared as a set: this walk is over the graph's traversal, which sorts by
	// node identity, so the order here is a property of a hash rather than of
	// the pattern. The order that *is* meaningful - which source wins when two
	// write the same path - is the COPY chain, and
	// TestAPatternExpandsInAFixedOrder asserts on that.
	sort.Strings(got)

	want := []string{"scripts/one.sh", "scripts/two.sh"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("copied %v, want %v", got, want)
	}
}

// The order a pattern expands in is fixed, because it reaches the cache key.
//
// A directory listing is not ordered, and two machines that expanded the same
// pattern differently would key the same build two ways - so neither would ever
// hit the other's cache, for no reason anyone could see.
func TestAPatternExpandsInAFixedOrder(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{
		testFileB: "b\n", testFileA: "a\n", "c.txt": "c\n",
	})

	for range 5 {
		p, err := interp.Build(versioned+
			"\nmain:\n    FROM alpine:3.22\n    COPY *.txt /dst/\n",
			testMain, interp.WithContext(ctx))
		if err != nil {
			t.Fatal(err)
		}

		// The COPY chain, not the source nodes: each copy stands on the one
		// before it, so the chain is where the order is meaningful - and the
		// order two sources are applied in decides which one wins when they
		// write the same path.
		var got []string

		for n := p.Graph.Root; n != nil; {
			if n.Op.Kind == ir.OpFile {
				got = append([]string{n.Op.Args[0]}, got...)
			}

			if len(n.Inputs) == 0 {
				break
			}

			n = n.Inputs[0]
		}

		if strings.Join(got, ",") != "a.txt,b.txt,c.txt" {
			t.Fatalf("expanded to %v, want sorted", got)
		}
	}
}

// A pattern that matches nothing says so, rather than reporting a file named
// `*.sh` as missing.
func TestAPatternThatMatchesNothingSaysSo(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testFileA: "a\n"})

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    COPY *.sh /dst/\n",
		testMain, interp.WithContext(ctx))
	if err == nil {
		t.Fatal("a pattern matching nothing was accepted")
	}

	for _, want := range []string{"*.sh", "matches nothing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%s", want, err)
		}
	}
}

// A pattern cannot reach outside the build context, however it is written.
func TestAPatternCannotEscapeTheContext(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{testFileA: "a\n"})

	for _, src := range []string{"../*", "../../*.txt", "sub/../../*"} {
		t.Run(src, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n    COPY "+src+" /dst/\n",
				testMain, interp.WithContext(ctx))
			if err == nil {
				t.Fatalf("%q was accepted", src)
			}

			if !strings.Contains(err.Error(), "context") {
				t.Errorf("the refusal does not say what is wrong:\n%s", err)
			}
		})
	}
}
