package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A flag's value is expanded like any other word.
//
// Found by a corpus sweep on Linux. `examples/rust` fails with:
//
//	RUN --mount type=(none) is not supported by the native engine
//	  (.../lib/3.0.4/rust/Earthfile:62)
//
// and line 62 is:
//
//	RUN --mount=$EARTHLY_RUST_CARGO_HOME_CACHE --mount=$EARTHLY_RUST_TARGET_CACHE \
//
// The whole mount specification is an argument, set by a `DO` a few lines
// above. `(none)` is what the parser writes when a spec has no `type=`, which
// is what an unexpanded `$VAR` amounts to - so the message names something the
// author never wrote and blames a construct that is supported.
//
// **This is the standard caching idiom of `earthly-lib`**, which the rust,
// python and node libraries all use, so it is not one example: it is every
// Earthfile that caches through the published functions.
func TestAFlagValueIsExpanded(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

probe:
    FROM alpine:3.22
    ARG spec=type=cache,target=/c
    RUN --mount=$spec echo hi
`

	_, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err == nil {
		return
	}

	if strings.Contains(err.Error(), "(none)") {
		t.Fatalf("the flag's value was not expanded, so its type was lost:\n%v", err)
	}

	t.Fatalf("a cache mount named by an argument was refused:\n%v", err)
}
