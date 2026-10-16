package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestIfExistsReachesTheStepForAnArtifactCopy.
//
// `COPY --if-exists +save/not_ok .` from a target whose `SAVE ARTIFACT
// --if-exists not_ok` produced nothing. The artifact is *declared*, so the plan
// is right to emit the copy - whether the file exists is a fact about the
// filesystem the producer left behind, and only the step can see it.
//
// The flag never reached the step. It is resolved in the interpreter for a
// context path, where the interpreter can look; for an artifact it cannot, and
// dropping it there turned a tolerated absence into "nothing in that target
// has it" from inside the guest.
//
// So it travels with the copy, and it is part of the key: a step that tolerates
// a missing source is not the same step as one that requires it, and the two
// must not share an entry.
func TestIfExistsReachesTheStepForAnArtifactCopy(t *testing.T) {
	t.Parallel()

	src := `
save:
    FROM alpine:3.22
    RUN touch ok
    SAVE ARTIFACT ok
    SAVE ARTIFACT --if-exists not_ok

use:
    FROM alpine:3.22
    COPY %s +save/not_ok .
    RUN true
`

	p, err := interp.Build(versioned+strings.Replace(src, "%s", "--if-exists", 1), "use")
	if err != nil {
		t.Fatal(err)
	}

	var seen bool

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpFile || !strings.Contains(n.Op.Args[0], "not_ok") {
			continue
		}

		seen = true

		if !n.Op.IfExists {
			t.Error("the copy does not tolerate a missing source, so a producer" +
				" that saved nothing fails the consumer from inside the guest")
		}
	}

	if !seen {
		t.Fatal("no copy of the artifact was planned")
	}

	// And without the flag the step requires it, so the two do not share a key.
	q, err := interp.Build(versioned+strings.Replace(src, "%s ", "", 1), "use")
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range q.Graph.Nodes() {
		if n.Op.Kind == ir.OpFile && strings.Contains(n.Op.Args[0], "not_ok") && n.Op.IfExists {
			t.Error("a copy written without --if-exists tolerates a missing source")
		}
	}
}
