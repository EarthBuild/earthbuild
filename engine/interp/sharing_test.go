package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The three sharing modes, and what each one means to a step.
//
// They were all refused but `locked`, on the honest grounds that accepting a
// mode while providing a different one is guessing about concurrency (E427).
// Both mechanisms now exist - a per-id lock for `locked`, and an ephemeral mount
// for a directory nothing else can see - so the guess is not required and the
// modes can be what the reference says they are (E432).
//
// | mode      | what a step gets                                    |
// | --------- | --------------------------------------------------- |
// | `locked`  | the shared directory, one step in it at a time      |
// | `shared`  | the shared directory, several steps at once         |
// | `private` | a directory of its own, thrown away with the step   |
//
// One subtest per row, so a mode that stops meaning what the table says fails
// against the table rather than against somebody's memory of it.
func TestTheThreeSharingModes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		mode      string
		shared    bool
		exclusive bool
	}{
		{name: "unstated is locked", shared: true, exclusive: true},
		{name: "locked", mode: "--sharing=locked", shared: true, exclusive: true},
		{name: "shared", mode: "--sharing=shared", shared: true},
		{name: "private", mode: "--sharing=private"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan, err := interp.Build("VERSION 0.8\nbuild:\n    FROM alpine\n"+
				"    CACHE "+tc.mode+" /root/.m2\n    RUN mvn package\n", "build")
			if err != nil {
				t.Fatalf("%v", err)
			}

			var m ir.Mount

			for _, n := range plan.Graph.Nodes() {
				if len(n.Op.Mounts) > 0 {
					m = n.Op.Mounts[0]
				}
			}

			if m.Target == "" {
				t.Fatal("no step carries the cache mount")
			}

			// A private cache is nobody else's: it has no shared directory to
			// name, and is made and removed for this step.
			if got := m.ID != ""; got != tc.shared {
				t.Errorf("shared directory = %v, want %v (id %q)", got, tc.shared, m.ID)
			}

			if got := !m.Ephemeral; got != tc.shared {
				t.Errorf("ephemeral = %v, want %v", !got, !tc.shared)
			}

			if m.Exclusive != tc.exclusive {
				t.Errorf("exclusive = %v, want %v", m.Exclusive, tc.exclusive)
			}
		})
	}
}

// A mode nobody has heard of is still refused.
//
// The reason the old refusal existed remains: accepting a word this engine does
// not implement would answer a question about concurrency with a guess.
func TestAnUnknownSharingModeIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build("VERSION 0.8\nbuild:\n    FROM alpine\n"+
		"    CACHE --sharing=whenever /root/.m2\n    RUN true\n", "build")
	if err == nil {
		t.Fatal("an unknown sharing mode was accepted")
	}

	if !strings.Contains(err.Error(), "whenever") {
		t.Errorf("the refusal does not quote what was written: %v", err)
	}
}
