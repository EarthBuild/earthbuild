package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Both spellings of `--mount` mean the same thing.
//
// A corpus sweep on Linux refused `examples/rust` with:
//
//	RUN --mount type=(none) is not supported by the native engine
//
// `(none)` is what the parser writes when the spec has no `type=`, and the
// Earthfile it came from - a function in `github.com/EarthBuild/lib/rust` -
// certainly names one. So either the type is being lost or the spelling is.
//
// Dockerfiles and Earthfiles both permit `--mount=type=cache,...` as well as
// `--mount type=cache,...`, and a flag parser that strips the flag name but not
// the `=` leaves `=type=cache`, whose first `key=value` split yields an empty
// key. The type is then missing and the message names nothing the author wrote.
func TestBothSpellingsOfAMountAreOneMount(t *testing.T) {
	t.Parallel()

	for _, spec := range []string{
		`--mount type=cache,target=/c`,
		`--mount=type=cache,target=/c`,
	} {
		t.Run(spec, func(t *testing.T) {
			t.Parallel()

			src := "VERSION 0.8\n\nprobe:\n    FROM alpine:3.22\n    RUN " + spec + " echo hi\n"

			_, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
			if err == nil {
				return
			}

			if strings.Contains(err.Error(), "(none)") {
				t.Errorf("the mount's type was lost, so the refusal names nothing:\n%v", err)
			}

			t.Errorf("a cache mount was refused:\n%v", err)
		})
	}
}
