package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Every command that takes flags must read them as flags.
//
// RUN and IF each shipped with their options unparsed, so the flag became the
// first word of the command or the condition. Twice is a pattern, so this asks
// the question of every command with an options type at once: a flag is either
// honoured or refused *by name*, and never quietly becomes part of a value.
//
// The check is on the diagnostic rather than the graph, because both outcomes
// are acceptable and only the third is not. A refusal naming the flag is fine.
// A refusal complaining about a path called `--if-exists`, or a build that
// quietly saved an artifact under that name, is the defect.
func TestNoCommandSwallowsItsOwnFlags(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{
		testSourceFile:   "x\n",
		testLibEarthfile: versioned + "\nthing:\n    FROM alpine\n",
	})

	// witness is a word from the construct itself, which must be somewhere in
	// the plan. The assertions below are all *negative* - the flag must not
	// appear - and a negative assertion in a loop is satisfied by an empty
	// loop: if the construct stopped reaching the graph at all, every check
	// here would pass having examined nothing. The witness is what makes the
	// silence mean something.
	for _, tc := range []struct {
		name    string
		body    string
		flag    string
		witness string
	}{
		{"SAVE ARTIFACT --if-exists", "    RUN make\n    SAVE ARTIFACT --if-exists /out /dst\n", "--if-exists", testOutDir},
		{testForcedArtifact, "    RUN make\n    SAVE ARTIFACT --force /out /dst\n", "--force", testOutDir},
		{"COPY --dir", "    COPY --dir src.txt /dst/\n", "--dir", testSourceFile},
		{"RUN --no-cache", "    RUN --no-cache make\n", "--no-cache", "make"},
		{"IF --no-cache", "    IF --no-cache [ \"a\" = \"a\" ]\n        RUN yes\n    END\n", "--no-cache", "yes"},
		// Flags come before the variable, which is where the documentation puts
		// them; after IN they are items, and looping over the literal text is
		// the only reading available.
		{"FOR --sep", "    FOR --sep=, x IN a,b\n        RUN got-$x\n    END\n", "--sep", "got-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+"\nmain:\n    FROM alpine:3.22\n"+tc.body,
				testMain, interp.WithContext(ctx))
			if err != nil {
				// Refused is a fine answer, so long as the refusal is *about*
				// the flag rather than about a file or target named after it.
				if !strings.Contains(err.Error(), tc.flag) {
					t.Fatalf("refused without naming %s, so the flag was read as a value:\n%s", tc.flag, err)
				}

				for _, wrong := range []string{"is not in the build context", "names no target", "never imported"} {
					if strings.Contains(err.Error(), wrong) {
						t.Errorf("the flag was read as a value:\n%s", err)
					}
				}

				return
			}

			// Accepted: then the construct is in the plan, and its flag is not.
			// The first half is what stops the second from being vacuous.
			var present bool

			for _, n := range p.Graph.Nodes() {
				if strings.Contains(strings.Join(n.Op.Args, " "), tc.witness) {
					present = true
				}
			}

			for _, a := range p.Artifacts {
				if strings.Contains(a.Path, tc.witness) || strings.Contains(a.LocalDest, tc.witness) {
					present = true
				}
			}

			if !present {
				t.Fatalf("nothing in the plan mentions %q, so this row checks that"+
					" %s is absent from a graph the construct never reached:\n%s",
					tc.witness, tc.flag, describe(p.Graph.Nodes()))
			}

			// Accepted: then it must not have travelled into any operation.
			for _, n := range p.Graph.Nodes() {
				if joined := strings.Join(n.Op.Args, " "); strings.Contains(joined, tc.flag) {
					t.Errorf("%s reached the operation: %q", tc.flag, joined)
				}
			}

			// Nor into an artifact, which is where SAVE ARTIFACT's flags went
			// and where checking only operations could not see them. A test
			// that looks in one place finds bugs in one place.
			for _, a := range p.Artifacts {
				if strings.Contains(a.Path, tc.flag) || strings.Contains(a.LocalDest, tc.flag) {
					t.Errorf("%s reached an artifact: path=%q dest=%q", tc.flag, a.Path, a.LocalDest)
				}
			}
		})
	}
}

// An IMPORT's flags are not part of the path it names.
func TestImportFlagsAreNotThePath(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{
		testLibEarthfile: versioned + "\nthing:\n    FROM alpine:3.22\n    RUN in-the-lib\n",
	})

	p, err := interp.Build(versioned+
		"\nIMPORT --allow-privileged ./lib AS lib\n\nmain:\n    FROM lib+thing\n",
		testMain, interp.WithContext(ctx))
	if err != nil {
		if !strings.Contains(err.Error(), "--allow-privileged") {
			t.Fatalf("refused without naming the flag, so it was read as the path:\n%s", err)
		}

		return
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "in-the-lib") {
		t.Errorf("the import did not resolve:\n%s", got)
	}
}
