package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A Dockerfile that only exists once something has been built is refused, and
// says so.
//
// `FROM DOCKERFILE +create-dockerfile/` names a target as the *context*, which
// this engine supports - and, with no `-f`, the Dockerfile itself is expected
// inside that target's output. This engine parses the Dockerfile while planning,
// so it cannot be a file that does not exist yet, which is written at the point
// where the file is read:
//
//	The Dockerfile itself always comes from this machine, beside the
//	Earthfile ... looking for it in the target's output would need that target
//	built before anything could be parsed.
//
// That constraint was true and unsaid. The engine read `Dockerfile` beside the
// Earthfile instead, and on a case-insensitive filesystem that found the corpus's
// `tests/dockerfile/` **directory** and reported `is a directory` - a diagnosis
// about the wrong file, in a directory the author never named (E478).
func TestADockerfileInsideATargetsOutputIsRefusedByName(t *testing.T) {
	t.Parallel()

	for name, src := range map[string]string{
		// The context is a target and nothing says where the Dockerfile is, so
		// it is in that target's output.
		"context from a target": "\nmain:\n    FROM DOCKERFILE +gen/\n" + genDockerfile,
		// `-f` names one explicitly, and names an artifact.
		"-f names an artifact": "\nmain:\n    FROM DOCKERFILE -f +gen/other.Dockerfile .\n" +
			genDockerfile,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+src, testMain)
			if err == nil {
				t.Fatal("a Dockerfile that does not exist yet was parsed anyway")
			}

			// The phrase rather than the name: `-f +gen/other.Dockerfile`
			// already put `+gen` in the old message, as part of a path it had
			// joined onto the project directory - so asserting the name alone
			// passed against the diagnosis this test exists to replace.
			for _, want := range []string{
				"FROM DOCKERFILE", "+gen",
				"and this plan was made without anywhere to build it",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refused with %q, which does not name %s", err, want)
				}
			}

			// The old message named a file the Earthfile never mentions.
			if strings.Contains(err.Error(), "is a directory") {
				t.Errorf("refused with %q, which is about the wrong file", err)
			}
		})
	}
}

// A Dockerfile on this machine still works, with a target as the context.
//
// The half that keeps the refusal narrow: `-f ./Dockerfile +gen/*` is the
// supported shape - the context is what the build reads and the Dockerfile is
// what says how to read it - and a refusal that took this with it would remove a
// capability the engine has.
func TestATargetContextWithALocalDockerfileStillPlans(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{
		"Dockerfile": "FROM alpine:3.22\nCOPY . /src\n",
	})

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM DOCKERFILE -f ./Dockerfile +gen/*\n"+genDockerfile,
		testMain, interp.WithContext(dir))
	if err != nil {
		t.Errorf("a local Dockerfile with a target's output as its context was"+
			" refused: %v", err)
	}
}

const genDockerfile = "\ngen:\n    FROM alpine:3.22\n" +
	"    RUN echo 'FROM alpine:3.22' > Dockerfile\n    SAVE ARTIFACT Dockerfile\n"
