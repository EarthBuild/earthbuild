package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A refused flag is told what it asks for, not only that it is refused.
//
// The refusal today reads:
//
//	COPY --keep-own is not supported by the native engine (Earthfile:5)
//	  to build this now, use --engine=buildkit
//
// which tells a reader that the door is shut and nothing about whether they
// wanted to go through it. That is the E68 shape one construct over: a message
// naming the refusal and not the thing refused, so the only way forward is to
// go and read the reference documentation - the documentation *this repository
// ships*, three directories away from the code doing the refusing.
//
// It matters more than tidiness because the refusal list has already been wrong
// in the expensive direction. `--keep-ts` was refused while this engine did
// exactly what it asks (E34): a reader told what the flag meant would have seen
// that immediately, and a reader told only "not supported" filed nothing and
// used the other engine. **A refusal that explains itself is a refusal that can
// be argued with**, and the arguments are how the list gets checked.
func TestARefusedFlagSaysWhatItAsksFor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
		// want is a phrase from the flag's own description in
		// docs/earthfile/earthfile.md, which is where these come from.
		want string
	}{
		{
			name: "RUN --privileged",
			body: "    RUN --privileged echo hi\n",
			want: "privileged capabilities",
		},
		{
			// `RUN --ssh` was here, as the case whose wanted phrase appeared in
			// the flag's own name - *"a wanted phrase that appears in the
			// flag's own name tests nothing"*. It is implemented now (E466), so
			// the case is `RUN --aws`, which asks for credentials this engine
			// has no way to obtain and whose refusal must therefore say what it
			// is about rather than repeat the flag.
			name: "RUN --aws",
			body: "    RUN --aws echo hi\n",
			want: "credential",
		},
		{
			// The one where refusing is a position rather than a gap. `--force`
			// exists to permit a save that writes outside the project
			// directory, which is the thing insideProject was written to stop.
			// Saying "not supported" of a flag this engine will never have
			// invites somebody to implement it.
			name: testForcedArtifact,
			body: "    RUN mkdir /out\n    SAVE ARTIFACT --force /out AS LOCAL out\n",
			want: "outside",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := "VERSION 0.8\n\nprobe:\n    FROM alpine:3.22\n" + tc.body

			_, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
			if err == nil {
				t.Fatalf("%s was accepted, so this case refuses nothing", tc.name)
			}

			got := err.Error()

			if !strings.Contains(got, tc.want) {
				t.Errorf("the refusal does not say what the flag asks for (wanted %q):\n%s", tc.want, got)
			}

			// The explanation is an addition, not a replacement. A message that
			// gained a description and lost the place or the way out would be a
			// worse message than the one it replaced.
			for _, keep := range []string{"Earthfile:", "--engine=buildkit"} {
				if !strings.Contains(got, keep) {
					t.Errorf("the refusal no longer contains %q:\n%s", keep, got)
				}
			}
		})
	}
}

// A refusal with nothing recorded about the flag is still a clean refusal.
//
// The descriptions are quoted from this repository's own reference
// documentation, and a flag that is not in it gets no line rather than a line
// somebody invented. Silence is the honest answer; **a description nobody
// checked is worse than none**, because a wrong one sends the reader somewhere
// there is nothing to find and they believe it on the way.
//
// So the shape has to survive an absent entry: no blank line, no dangling
// indent, no "asks for ." - the failure mode of every message built by
// concatenation.
func TestARefusalWithNoRecordedMeaningIsStillWellFormed(t *testing.T) {
	t.Parallel()

	// STOPSIGNAL is refused as a whole command, so nothing about a flag
	// applies to it at all.
	//
	// It was HEALTHCHECK until that was implemented (E486), and the swap is the
	// point of this comment: a test that borrows an unsupported construct as a
	// *fixture* goes stale the day somebody supports it, and says so - three
	// guards did here, which is why the swap took a minute rather than an
	// afternoon.
	src := `VERSION 0.8

probe:
    FROM alpine:3.22
    STOPSIGNAL SIGTERM
`

	_, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err == nil {
		t.Fatal("STOPSIGNAL was accepted, so this case refuses nothing")
	}

	got := err.Error()

	for line := range strings.SplitSeq(got, "\n") {
		if strings.TrimSpace(line) == "" {
			t.Errorf("the refusal has an empty line in it:\n%q", got)
		}

		if strings.HasSuffix(strings.TrimSpace(line), "asks for") {
			t.Errorf("the refusal has an explanation with nothing in it:\n%q", got)
		}
	}
}
