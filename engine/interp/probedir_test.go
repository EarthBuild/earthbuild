package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// dirRecorder remembers the working directory a probe was given.
type dirRecorder struct {
	dirs   []string
	output string
}

func (r *dirRecorder) run(_ []string, _ *ir.Node, dir, _ string) (interp.Result, error) {
	r.dirs = append(r.dirs, dir)

	return interp.Result{Exit: 0, Output: r.output}, nil
}

// A probe runs where the build is, not at the filesystem root.
//
// `WORKDIR /var/app` then `SAVE IMAGE app:$(cat version)` reads a file that a
// COPY on the line above put in /var/app. Running the probe at `/` looked for a
// file the Earthfile never mentions, and reported it as the command failing -
// which reads as a broken Earthfile rather than a working directory nobody
// carried.
//
// A probe observes the build state, and *where* it observes from is part of
// that state.
func TestAProbeRunsInTheWorkingDirectory(t *testing.T) {
	t.Parallel()

	r := &dirRecorder{output: "1.2.3\n"}

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WORKDIR /var/app
    RUN make-the-version > version
    SAVE IMAGE app:$(cat version)
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.dirs) == 0 {
		t.Fatal("nothing was run to find the value")
	}

	if r.dirs[0] != "/var/app" {
		t.Errorf("the probe ran in %q, want /var/app", r.dirs[0])
	}
}

// A condition is the same: it decides against the build as it stands.
func TestAConditionIsDecidedInTheWorkingDirectory(t *testing.T) {
	t.Parallel()

	r := &dirRecorder{}

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WORKDIR /srv
    IF [ -f config ]
        RUN use-the-config
    END
`, testMain, interp.WithCommands(r.run))
	if err != nil {
		t.Fatal(err)
	}

	if len(r.dirs) == 0 || r.dirs[0] != "/srv" {
		t.Errorf("the condition was decided in %q, want /srv", r.dirs)
	}
}
