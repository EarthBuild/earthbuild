package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A bare WITH DOCKER plans its body, and every step in it asks for a daemon.
//
// The bare form is a quarter of the corpus's uses - 96 of 892 lines - and the
// bodies run `docker run`, `docker inspect` and `docker images`. It is the
// smallest slice of this construct that is worth anything, because there is no
// slice at all that does not need a daemon.
func TestABareWithDockerMarksItsBody(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN before
    WITH DOCKER
        RUN docker images
        RUN docker run --rm alpine true
    END
    RUN after
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{testDockerImages: true, "RUN docker run --rm alpine true": true}
	seen := map[string]bool{}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		d := n.Meta.Description

		switch {
		case want[d]:
			seen[d] = true

			if !n.Op.Docker {
				t.Errorf("%q is inside WITH DOCKER and was not given a daemon", d)
			}
		case d == "RUN before" || d == testAfter:
			if n.Op.Docker {
				t.Errorf("%q is outside the block and was given a daemon", d)
			}
		}
	}

	for d := range want {
		if !seen[d] {
			t.Errorf("%q is not in the graph:\n%s", d, describe(p.Graph.Nodes()))
		}
	}
}

// The build carries on from the block, so what follows stands on what it did.
func TestWhatFollowsAWithDockerBlockStandsOnIt(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER
        RUN docker images
    END
    RUN after
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description == testAfter && !reaches(n, testDockerImages) {
			t.Error("the rest of the build does not follow the block")
		}
	}
}

// The options are refused by name until they are implemented, rather than
// accepted and ignored.
//
// `--load` builds another target and puts its image in the daemon; a block that
// accepted the flag and did nothing would run `docker run` against an image
// that is not there, and blame the Earthfile.
func TestWithDockerOptionsAreRefusedByName(t *testing.T) {
	t.Parallel()

	for _, opt := range []string{
		// --load, --pull and --compose are implemented; see withload_test.go,
		// withpull_test.go and withcompose_test.go. What is left changes what
		// the daemon itself is rather than what is in it.
		// What is left changes what the daemon itself is, rather than what is
		// in it or what built it.
		//
		// `--allow-privileged` was here and is now accepted: it grants a
		// permission to a *referenced target*, and this engine refuses
		// privileged execution wherever it appears - so the grant is never
		// taken up and refusing it as well is two answers to one question
		// (E476).
	} {
		t.Run(opt, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER `+opt+`
        RUN docker images
    END
`, testMain)
			if err == nil {
				t.Fatalf("WITH DOCKER %s was accepted and its option ignored", opt)
			}

			name, _, _ := strings.Cut(opt, "=")
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the refusal does not name %q:\n%s", name, err)
			}
		})
	}
}

// WITH anything else is still refused: DOCKER is the only form there is.
func TestWithSomethingElseIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH SOMETHING
        RUN true
    END
`, testMain)
	if err == nil {
		t.Fatal("WITH SOMETHING was accepted")
	}
}
