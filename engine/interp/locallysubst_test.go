package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A `$(...)` in a LOCALLY target is asked of the host, not of an image.
//
// `LET tree = "$(git rev-parse HEAD^{tree})"` straight after LOCALLY was handed
// a nil base - LOCALLY produces no step - and the runner refused it as "there
// is no filesystem to evaluate it against", while earthly runs it on the host.
// After a FROM it was handed that image, which is worse: an answer about the
// wrong machine, indistinguishable from a right one.
//
// So the base is a host anchor: a host step with no command, carrying where
// and with what environment the probe runs. An earlier host RUN is its input,
// so the runner can still refuse to run that a second time.
func TestALocallySubstitutionIsAskedOfTheHost(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, src string
		hostInput bool
	}{
		{"first line", "\nmain:\n    LOCALLY\n    LET v = \"$(pwd)\"\n    RUN echo $v\n", false},
		{"after a FROM", "\nmain:\n    FROM alpine:3.22\n    RUN prepare\n    LOCALLY\n" +
			"    LET v = \"$(pwd)\"\n    RUN echo $v\n", false},
		{"after a host RUN", "\nmain:\n    LOCALLY\n    RUN touch f\n    LET v = \"$(cat f)\"\n" +
			"    RUN echo $v\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &recorder{result: true, output: "out\n"}

			_, err := interp.Build(versioned+tc.src, testMain, interp.WithCommands(r.run))
			if err != nil {
				t.Fatal(err)
			}

			if len(r.bases) != 1 {
				t.Fatalf("the substitution was run %d times, want 1", len(r.bases))
			}

			base := r.bases[0]
			if base == nil || base.Op.Kind != ir.OpHost || len(base.Op.Args) != 0 {
				t.Fatalf("a LOCALLY substitution was given %s, want a host anchor", describeOne(base))
			}

			hostIn := len(base.Inputs) == 1 && base.Inputs[0].Op.Kind == ir.OpHost
			if hostIn != tc.hostInput {
				t.Errorf("anchor inputs %s: an earlier host RUN must be its only input, and nothing else",
					describe(base.Inputs))
			}
		})
	}
}

func describeOne(n *ir.Node) string {
	if n == nil {
		return "nothing"
	}

	return describe([]*ir.Node{n})
}
