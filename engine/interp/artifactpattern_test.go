package interp_test

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A saved *pattern* keeps the directory as its destination.
//
// `SAVE ARTIFACT ./*` declares a pattern whose matches are known only once the
// producing target's filesystem exists, so the plan cannot name them. Joining
// the pattern to the destination produced `out/*` - a concrete file name with a
// star in it - and the copy landed one file called `*`.
//
// `tests/platform` is built on this shape: `+run` saves `./*` and the target
// above copies `+run/*` into a directory per platform, fifteen times. Every
// assertion after it then read a path nobody wrote a rule about (E960).
//
// A saved *name* still lands under that name, which is the case the comment at
// this line was written for and the case the ENTRYPOINT two lines later depends
// on.
func TestASavedPatternLandsInTheDirectoryAndANameLandsUnderIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, saves, wantDest string
	}{{
		name:     "a pattern leaves the naming to the copy",
		saves:    "    SAVE ARTIFACT ./*\n",
		wantDest: "out/",
	}, {
		name:     "a name is joined as before",
		saves:    "    SAVE ARTIFACT ./uname-m\n",
		wantDest: "out/uname-m",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+`
run:
    FROM alpine:3.22
    RUN uname -m > ./uname-m
`+tc.saves+`
main:
    FROM alpine:3.22
    COPY +run/* ./out/
`, "main")
			if err != nil {
				t.Fatalf("planning: %v", err)
			}

			var dests []string

			for _, n := range p.Graph.Nodes() {
				if n.Op.Kind == ir.OpFile && len(n.Op.Args) == 2 {
					dests = append(dests, n.Op.Args[1])
				}
			}

			if !slices.Equal(dests, []string{tc.wantDest}) {
				t.Errorf("the copy lands at %q, want %q", dests, []string{tc.wantDest})
			}
		})
	}
}
