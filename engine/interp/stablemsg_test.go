package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Every refusal says the same thing every time.
//
// Three times in one session a map's iteration order reached the engine's
// output, and the third was inside an error message: the list of feature flags
// in `VERSION --x is not known` came straight out of a map, so the same
// Earthfile was refused two different ways depending on the run (E66).
//
// It was caught by `TestPlanningIsDeterministic`, which builds the repository's
// own targets and compares runs - about one run in six, because two orders
// agree half the time and the message only appears for some targets. **A
// property that is only checked probabilistically is not checked.**
//
// So this asks directly, and it asks the question the class is about: a message
// is part of what a build produces (I12), and a build whose record varies makes
// every tool that diffs two builds report noise.
//
// Twenty repetitions rather than two: Go randomises map iteration per loop, so
// a two-element list agrees half the time and this would be a coin flip. Twenty
// makes a stable-looking flake a one-in-five-hundred-thousand event.
func TestEveryRefusalSaysTheSameThingTwice(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		src  string
	}{
		{
			// The one that was wrong: it lists the flags this engine gates on.
			name: "an unknown VERSION flag",
			src: `VERSION --no-such-flag 0.8

probe:
    FROM alpine:3.22
    RUN echo hi
`,
		},
		{
			// Lists the commands this engine knows, which is a map of the same
			// shape one refusal over.
			name: "an unsupported command",
			src: `VERSION 0.8

probe:
    FROM alpine:3.22
    STOPSIGNAL SIGTERM
`,
		},
		{
			name: "an unsupported flag",
			src: `VERSION 0.8

probe:
    FROM alpine:3.22
    RUN --privileged echo hi
`,
		},
		{
			name: "a target that is not there",
			src: `VERSION 0.8

probe:
    FROM alpine:3.22
    BUILD +absent
`,
		},
		{
			name: "a construct that needs a feature",
			src: `VERSION 0.8

probe:
    FROM alpine:3.22
    TRY
        RUN false
    FINALLY
        RUN echo done
    END
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			first := ""

			for i := range 20 {
				_, err := interp.Build(tc.src, "probe", interp.WithPlatform("linux/arm64"))
				if err == nil {
					t.Fatalf("%s was accepted, so this case refuses nothing", tc.name)
				}

				if i == 0 {
					first = err.Error()

					continue
				}

				if err.Error() != first {
					t.Fatalf("the same file was refused two ways:\n  %s\n  %s", first, err.Error())
				}
			}

			// A refusal that says nothing would pass the comparison above by
			// being consistently empty, which is the way this kind of check
			// usually rots.
			if !strings.Contains(first, testEarthfile) && !strings.Contains(first, "probe") {
				t.Errorf("the refusal names neither the file nor the target:\n%s", first)
			}
		})
	}
}
