package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A builtin argument is refused wherever it is set, not only where E457 looked.
//
// `tests/builtin-args-invalid-default.earth` writes `ARG EARTHLY_VERSION="this
// is not possible"` and `builtin-args-invalid-pass.earth` writes `BUILD +t
// --EARTHLY_VERSION=...`; both exist to be refused, and the run gate caught this
// engine building both (E472). The refusal existed - it was reached from one
// path and not from these two, which is the same nothing from the file's side.
func TestABuiltinIsRefusedWhereverItIsSet(t *testing.T) {
	t.Parallel()

	for name, src := range map[string]string{
		"as a default": versioned +
			"\nmain:\n    FROM alpine:3.22\n" +
			"    ARG EARTHLY_VERSION=\"this is not possible\"\n" +
			"    RUN echo $EARTHLY_VERSION\n",
		"passed to a target": versioned +
			"\nmain:\n    BUILD +other --EARTHLY_VERSION=\"this is not possible\"\n" +
			"\nother:\n    FROM alpine:3.22\n    ARG EARTHLY_VERSION\n" +
			"    RUN echo $EARTHLY_VERSION\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := interp.Build(src, testMain)
			if err == nil {
				t.Fatalf("%s: the engine's answer was overwritten and nothing said so", name)
			}

			if !strings.Contains(err.Error(), "EARTHLY_VERSION") {
				t.Errorf("%s: refused with %q, which does not name the argument", name, err)
			}
		})
	}
}
