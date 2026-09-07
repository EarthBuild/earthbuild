package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestChmodReachesTheStepAndTheKey.
//
// `COPY --chmod=777 in/root .` gives the copied file that mode, and
// `tests/copy.earth+copy-chmod` copies one file four times asserting 644, 777,
// 600 and 666 in turn - the same source and destination each time, so the mode
// is the *only* thing that differs.
//
// It was refused as an option that changes what is copied, which was the right
// interim answer and is a gap rather than a decision. Unlike `--chown`, there
// is nothing here a store can fail to carry: a mode is part of a layer and this
// engine already keeps modes through SAVE ARTIFACT.
//
// **It has to be in the key, and that target is why.** Four copies of one file
// to one place, differing only in mode: keys that ignored the mode would make
// them one step and the second assertion would read the first's answer.
func TestChmodReachesTheStepAndTheKey(t *testing.T) {
	t.Parallel()

	src := `
main:
    FROM alpine:3.22
    COPY %s ./in/root .
    RUN true
`

	p, err := interp.Build(versioned+strings.Replace(src, "%s", "--chmod=777", 1), testMain,
		interp.WithContext(ctxWith(t, map[string]string{"in/root": "x"})))
	if err != nil {
		t.Fatalf("--chmod was refused: %v", err)
	}

	var seen bool

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile {
			continue
		}

		seen = true

		if n.Op.Chmod != "777" {
			t.Errorf("the copy carries mode %q, so the step cannot set it", n.Op.Chmod)
		}
	}

	if !seen {
		t.Fatal("no copy was planned")
	}

	// Two modes, one source and one destination: different steps, or the
	// second assertion reads the first's answer.
	q, err := interp.Build(versioned+strings.Replace(src, "%s", "--chmod=600", 1), testMain,
		interp.WithContext(ctxWith(t, map[string]string{"in/root": "x"})))
	if err != nil {
		t.Fatal(err)
	}

	if keyOfFirstFile(p) == keyOfFirstFile(q) {
		t.Error("777 and 600 share a key, so one build serves the other and the" +
			" mode asserted is whichever ran first")
	}
}

func keyOfFirstFile(p *interp.Plan) string {
	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile {
			return n.ID().String()
		}
	}

	return ""
}
