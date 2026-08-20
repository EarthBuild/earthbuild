package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `--allow-privileged` grants a permission this engine never uses.
//
// It lets a *referenced* target run privileged. This engine refuses privileged
// execution by name wherever it appears, so the permission is never taken up and
// declaring it changes nothing here - which is exactly the reasoning already
// written for `--allow-privileged-from-dockerfile` among the VERSION features,
// and already applied to `COPY --allow-privileged`.
//
// It was applied to COPY and not to BUILD, FROM, DO or WITH DOCKER: one flag,
// one meaning, five commands, two answers. Five corpus targets were refused for
// it, and the refusal read as a gap in the engine rather than as the position it
// is (E476).
//
// The asymmetry that decides which way to go is E34's: refusing something
// already implemented costs a working build, accepting something not implemented
// costs a wrong one. Nothing is granted here that was not already refused at the
// point of use, which is what the second half of this test is for.
func TestAllowPrivilegedIsAcceptedWhereverItIsWritten(t *testing.T) {
	t.Parallel()

	for name, src := range map[string]string{
		"BUILD": "\nmain:\n    FROM alpine:3.22\n    BUILD --allow-privileged +dep\n" + privDep,
		"FROM":  "\nmain:\n    FROM --allow-privileged +dep\n    RUN echo hi\n" + privDep,
		"COPY":  "\nmain:\n    FROM alpine:3.22\n    COPY --allow-privileged +dep/x .\n" + privDep,
		"DO":    "\nmain:\n    FROM alpine:3.22\n    DO --allow-privileged +HELPER\n" + privFn,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := interp.Build(versioned+src, testMain); err != nil {
				t.Errorf("%s --allow-privileged was refused: %v"+
					"\n  it grants a permission this engine never takes up", name, err)
			}
		})
	}
}

// And the permission is still not granted.
//
// The half that makes accepting the flag safe: a step that asks to be privileged
// is refused by name whether or not somebody wrote the flag that would have
// allowed it. **A permission accepted is not a permission granted**, and without
// this assertion the change above would be exactly that.
func TestAllowPrivilegedDoesNotMakeAStepPrivileged(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    BUILD --allow-privileged +dep\n"+
		"\ndep:\n    FROM alpine:3.22\n    RUN --privileged mount -t proc none /proc\n"+
		"    SAVE ARTIFACT /etc/hostname x\n", testMain)
	if err == nil {
		t.Fatal("a privileged step ran because a caller wrote --allow-privileged")
	}

	if !strings.Contains(err.Error(), "--privileged") {
		t.Errorf("refused with %q, which does not name the thing refused", err)
	}
}

const privDep = "\ndep:\n    FROM alpine:3.22\n    RUN echo x > /x\n" +
	"    SAVE ARTIFACT /x x\n"

const privFn = "\nHELPER:\n    FUNCTION\n    RUN echo helping\n"
