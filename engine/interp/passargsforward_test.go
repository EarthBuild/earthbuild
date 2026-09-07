package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `--pass-args` forwards what a recipe was *given*, not only what it declared.
//
// A function that declares nothing is the case, and the corpus has one on
// purpose: `tests/pass-args-via-function-with-override`'s middle file says so in
// a comment - *"This file doesn't define any ARGs, and is here to ensure all
// ARGs passed from the caller get re-passed to the final build target"*. Inside
// it the values are correctly invisible, because a function's scope holds what
// it declared; passing them on is a different question, and `rs.args` cannot
// answer it.
//
// `passable` exists for exactly this and says so: it was written for `DO`, where
// the same gap dropped an argument a wrapper never used (E867, E896a). The
// `--pass-args` sites went on forwarding `rs.args` alone (E950).
//
// The chain is three deep because two is not enough to show it: the value has to
// pass *through* a recipe that never names it.
func TestPassArgsForwardsWhatWasSuppliedAndNotOnlyWhatWasDeclared(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{
		"sub/Earthfile": versioned + `
FUNC2:
  FUNCTION
  BUILD --pass-args ./submarine+test
`,
		"sub/submarine/Earthfile": versioned + `
test:
  FROM alpine:3.22
  ARG --required MY_ARG
  ARG --required EXTRA_ARG
  RUN test "$MY_ARG" = "defaultvalue"
  RUN test "$EXTRA_ARG" = "super extra yes please"
`,
	})

	_, err := interp.Build(versioned+`
test:
  FROM alpine:3.22
  ARG MY_ARG=defaultvalue
  DO --pass-args +FUNC1 --EXTRA_ARG="yes please"

FUNC1:
  FUNCTION
  ARG MY_ARG=wrongdefaultvalue
  ARG EXTRA_ARG
  DO --pass-args ./sub+FUNC2 --EXTRA_ARG="super extra $EXTRA_ARG"
`, "test", interp.WithContext(dir))
	if err != nil {
		t.Errorf("an argument passed through a function that declares none was lost:\n%v", err)
	}
}
