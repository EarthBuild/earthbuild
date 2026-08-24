package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/env"
)

// The published container declares which engine it runs.
//
// `+earthly-docker` is built `FROM ./buildkitd+buildkitd`: the image *is* a
// buildkitd with the CLI beside it, and its entrypoint starts that daemon. A
// CLI in there which then defaulted to the native engine would boot a daemon it
// never spoke to - and, because the native engine builds only from a checkout,
// would refuse every remote target with
//
//	--engine=native cannot build github.com/...+hello: build it from a
//	checkout, or use --engine=buildkit
//
// which is what happened. The workflow sets EARTH_ENGINE for the *job*, and a
// job's environment does not cross into `docker run`; only the image can say
// this about itself.
//
// Checked here rather than left to the image test, which takes twelve minutes
// to say so and only runs on a full CI pass.
func TestThePublishedImageSaysWhichEngineItRuns(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join("..", "..", "Earthfile"))
	if err != nil {
		t.Fatal(err)
	}

	// Derived rather than spelled, so a renamed prefix moves this with it.
	want := "ENV " + env.Prefix + "ENGINE=buildkit"

	body := targetBody(t, string(b), "earthly-docker")

	if !strings.Contains(body, want) {
		t.Errorf("+earthly-docker does not set %s; it ships a buildkitd and"+
			" starts it, so a CLI defaulting to native would refuse every"+
			" remote target:\n%s", want, body)
	}
}

// targetBody is the indented body of one Earthfile target.
func targetBody(t *testing.T, src, target string) string {
	t.Helper()

	lines := strings.Split(src, "\n")

	var (
		body []string
		in   bool
	)

	for _, l := range lines {
		if strings.HasPrefix(l, target+":") {
			in = true

			continue
		}

		if !in {
			continue
		}

		// A target ends at the next line that starts in column zero and is not
		// blank - a comment introducing the next target included.
		if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			break
		}

		body = append(body, l)
	}

	if len(body) == 0 {
		t.Fatalf("no target %q in the Earthfile", target)
	}

	return strings.Join(body, "\n")
}
