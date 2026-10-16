package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `RUN --aws` needs the file to ask for it, and makes the step uncacheable.
//
// **The flag hands the invoking user's AWS credentials to a build step.** That
// is a capability rather than a convenience, so it is gated the way the
// reference gates it - `VERSION --run-with-aws` - and a file that uses it
// without saying so is refused rather than quietly given credentials.
//
// Uncacheable for the reason `--secret` is: the credentials are not in the key
// and must not be, so a step that ran with one set of them cannot serve a step
// asking with another. A cached `RUN --aws` would be a step reusing somebody
// else's authorisation.
func TestRunWithAWSIsGatedAndUncacheable(t *testing.T) {
	t.Parallel()

	const recipe = "\nmain:\n    FROM alpine:3.22\n    RUN --aws env\n"

	t.Run("refused without the VERSION flag", func(t *testing.T) {
		t.Parallel()

		_, err := interp.Build(versioned+recipe, testMain)
		if err == nil {
			t.Fatal("RUN --aws was accepted by a file that did not ask for it")
		}

		for _, want := range []string{"--run-with-aws", "VERSION"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not mention %q", err, want)
			}
		}
	})

	t.Run("accepted with it, and not cached", func(t *testing.T) {
		t.Parallel()

		g, err := interp.Build("VERSION --run-with-aws 0.8\n"+recipe, testMain)
		if err != nil {
			t.Fatalf("RUN --aws was refused by a file that asked for it: %v", err)
		}

		var seen bool

		for n := g.Graph.Root; n != nil; n = firstInput(n) {
			if n.Op.Kind != ir.OpExec || !n.Op.AWS {
				continue
			}

			seen = true

			if !n.Op.NoCache {
				t.Error("a RUN --aws step is cacheable" +
					"\n  the credentials are not in the key, so a cached result" +
					" would be one step reusing another's authorisation")
			}
		}

		if !seen {
			t.Error("no step recorded that it asked for AWS credentials")
		}
	})
}

// firstInput walks the single chain a linear recipe produces.
func firstInput(n *ir.Node) *ir.Node {
	if len(n.Inputs) == 0 {
		return nil
	}

	return n.Inputs[0]
}
