package interp_test

import (
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A refusal says which kind of refusal it is.
//
// Three sentinels divide them, and the division is the whole of what the sweeps
// report: `ErrNotProvided` is the caller's to fix, `ErrOnPurpose` is a decision
// nobody should fix, `ErrUnimplemented` is work. A refusal carrying none of them
// is a statement that the *Earthfile* is wrong.
//
// That last case is the default, and it is where a new refusal lands by
// accident: E478's - a Dockerfile produced by a target - was a gap written as a
// plain error, so a sweep reading the sentinels would have counted a piece of
// missing engine as a broken input file. **A category that is the default is a
// category things fall into** (E483).
func TestARefusalSaysWhichKindItIs(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		src  string
		want error
	}{
		// A construct this engine has not built. `STOPSIGNAL` is one: no
		// position on it, no capability withheld, simply absent.
		//
		// This was the Dockerfile-produced-by-a-target case until E487 gave the
		// caller a way to supply one - at which point it stopped being a gap and
		// became a capability this call withheld, which is the *other* sentinel.
		// A test that borrows a gap as a fixture goes stale the day somebody
		// closes it (E486 said the same about HEALTHCHECK).
		"a construct this engine has not built": {
			src:  "\nmain:\n    FROM alpine:3.22\n    STOPSIGNAL SIGTERM\n",
			want: interp.ErrUnimplemented,
		},
		"a construct refused by decision": {
			src: "\nmain:\n    FROM alpine:3.22\n" +
				"    RUN --privileged mount -t proc none /proc\n",
			want: interp.ErrOnPurpose,
		},
		"a Dockerfile only a build can produce": {
			src: "\nmain:\n    FROM DOCKERFILE +gen/\n" +
				"\ngen:\n    FROM alpine:3.22\n    RUN touch Dockerfile\n" +
				"    SAVE ARTIFACT Dockerfile\n",
			want: interp.ErrNotProvided,
		},

		"a value only running can produce": {
			src: "\nmain:\n    FROM alpine:3.22\n    ARG v=$(uname -r)\n" +
				"    RUN echo $v\n",
			want: interp.ErrNotProvided,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+tc.src, testMain)
			if err == nil {
				t.Fatal("planned, and this is meant to be refused")
			}

			if !errors.Is(err, tc.want) {
				t.Errorf("refused with %q\n  which carries no %v, so a sweep"+
					" reading the sentinels files it as a broken Earthfile",
					err, tc.want)
			}
		})
	}
}

// And an Earthfile that is genuinely wrong carries none of them.
//
// The other direction: if everything were labelled, the label would say nothing.
// A target that names no base has no filesystem to run in, and no engine can
// make that valid.
func TestABrokenEarthfileCarriesNoSentinel(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM +empty\n    RUN true\n\nempty:\n    ARG nothing\n",
		testMain)
	if err == nil {
		t.Fatal("a target with no base was planned")
	}

	if errors.Is(err, interp.ErrRefused) {
		t.Errorf("refused with %q, and marked it as the engine's gap or"+
			" decision - the Earthfile is what is wrong here", err)
	}
}
