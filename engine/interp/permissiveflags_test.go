package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A feature flag that only widens what is permitted is not a dialect.
//
// Both of these were refused, blocking ten targets in this repository's own
// `tests/` tree, and both grant a *permission* rather than introduce a
// construct:
//
//   - `--allow-without-earthly-labels` relaxes a check the reference makes on
//     images loaded into a WITH DOCKER. This engine makes no such check, so the
//     permission is one it already extends.
//   - `--allow-privileged-from-dockerfile` lets a `FROM DOCKERFILE` be
//     privileged. This engine refuses privileged execution wherever it appears,
//     by name, at the construct.
//
// **The rule: an engine stricter than the permission can ignore the flag that
// grants it, because the refusal still happens at the point of use.** That is
// the safe direction of E34's asymmetry - refusing something already
// implemented costs a working build, and accepting something not implemented
// costs a wrong one. Here nothing is accepted that was not already: the flag
// widens a door this engine keeps shut regardless.
//
// The test is therefore in two halves, and the second is the one that matters.
func TestAPermissionFlagIsAcceptedAndGrantsNothing(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{
		"--allow-without-earthly-labels",
		"--allow-privileged-from-dockerfile",
	} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			src := "VERSION " + flag + ` 0.8

probe:
    FROM alpine:3.22
    RUN echo hi
`

			_, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
			if err != nil {
				t.Errorf("a file naming %s was refused:\n%v", flag, err)
			}
		})
	}
}

// And the door stays shut.
//
// Accepting the flag must not accept what it grants elsewhere. If declaring
// `--allow-privileged-from-dockerfile` ever made `RUN --privileged` pass, the
// flag would have stopped being ignored and started being implemented - by
// accident, which is the only way that happens.
func TestAPermissionFlagDoesNotOpenWhatItPermits(t *testing.T) {
	t.Parallel()

	src := `VERSION --allow-privileged-from-dockerfile 0.8

probe:
    FROM alpine:3.22
    RUN --privileged echo hi
`

	_, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err == nil {
		t.Fatal("declaring the flag made privileged execution acceptable")
	}

	if !strings.Contains(err.Error(), testPrivilegedFlag) {
		t.Errorf("the refusal no longer names the construct:\n%v", err)
	}
}
