package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A construct behind a VERSION flag is refused unless the file asked for it.
//
// `VERSION` carries feature flags - `VERSION --try 0.8` - and they are a
// compatibility contract rather than decoration: they say which dialect a file
// is written in, so an Earthfile that builds somewhere builds everywhere.
//
// This engine read the version line and ignored the flags, so it accepted
// `TRY`/`CATCH`/`FINALLY` in a file that never opted into them - and the
// reference refuses that file outright. An Earthfile written against this
// engine would fail for everyone else, which is the quiet way a compatible
// implementation stops being one.
//
// The differential cannot find this: it compares builds *both* engines
// complete, and a construct only this engine accepts produces no reference
// build to compare against (E35).
func TestATryIsRefusedWithoutItsVersionFlag(t *testing.T) {
	t.Parallel()

	const body = `
probe:
    FROM alpine:3.22
    TRY
        RUN make
    FINALLY
        SAVE ARTIFACT /out.txt AS LOCAL out.txt
    END
`

	_, err := interp.Build("VERSION 0.8\n"+body, "probe")
	if err == nil {
		t.Fatal("TRY was accepted in a file that did not ask for it")
	}

	// The remedy is the flag, so the refusal has to name it.
	if !strings.Contains(err.Error(), "--try") {
		t.Errorf("the refusal does not name the flag that enables it:\n%v", err)
	}

	_, err = interp.Build("VERSION --try 0.8\n"+body, "probe")
	if err != nil {
		t.Errorf("TRY was refused in a file that asked for it: %v", err)
	}
}

// `base` names the implicit base recipe, so a target cannot be called that.
//
// The engine already knows the name is special - a reference to `+base` means
// the commands before the first target - but it accepted a *definition* of one,
// which the reference refuses outright. Same family as the flag above: an
// Earthfile this engine builds and no other will.
func TestATargetCannotBeCalledBase(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(`VERSION 0.8

base:
    FROM alpine:3.22
    RUN make

probe:
    FROM +base
    RUN report
`, "probe")
	if err == nil {
		t.Fatal("a target called base was accepted")
	}

	if !strings.Contains(err.Error(), "base") {
		t.Errorf("the refusal does not name the target:\n%v", err)
	}
}
