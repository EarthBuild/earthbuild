package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An ENV set inside a DO'd function reaches the caller.
//
// `DO` inlines a function into the target that calls it - it continues *this*
// filesystem rather than running a separate build - so what it sets is set. The
// engine's own comment on `do` says exactly that: "a function is a way of
// writing the same steps in one place, not a way of running a different build".
//
// Found by a corpus sweep on rootless Linux, three hops from the symptom:
//
//	RUN --mount type=(none) is not supported by the native engine
//	  (.../lib/3.0.4/rust/Earthfile:62)
//
// Line 62 is `RUN --mount=$EARTHLY_RUST_CARGO_HOME_CACHE`, and that variable is
// set by `ENV` inside `+SET_CACHE_MOUNTS_ENV`, called by `DO` eight lines
// earlier. Unexpanded it is empty, an empty mount specification has no `type=`,
// and the parser reports `(none)` - **a message naming something the author
// never wrote, about a construct that is supported.**
//
// It is the caching idiom of `earthly-lib`: the rust, python and node libraries
// all set their mounts this way, so this is not one example failing.
func TestAnEnvSetInsideADoReachesTheCaller(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

SETUP:
    FUNCTION
    ENV SPEC=type=cache,target=/c

probe:
    FROM alpine:3.22
    DO +SETUP
    RUN --mount=$SPEC echo hi
`

	plan, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		if strings.Contains(err.Error(), "(none)") {
			t.Fatalf("the function's ENV did not reach the caller, so the mount was empty:\n%v", err)
		}

		t.Fatalf("%v", err)
	}

	// And it really is a cache mount, not merely an accepted line.
	for _, n := range plan.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			if m.Target == "/c" {
				return
			}
		}
	}

	t.Error("no step has the cache mount the function's ENV described")
}

// And the ENV itself is visible to a later step.
//
// The narrower half, and the one that says whether this is about mounts or
// about `DO`: a function that sets an environment variable has set it for
// everything after the call.
func TestAnEnvSetInsideADoIsVisibleLater(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

SETUP:
    FUNCTION
    ENV GREETING=hello

probe:
    FROM alpine:3.22
    DO +SETUP
    RUN echo $GREETING
`

	plan, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatalf("%v", err)
	}

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && n.Op.Env["GREETING"] == testGreeting {
			return
		}
	}

	t.Error("a step after the DO does not carry the environment the function set")
}

// A function sees the environment its caller has set.
//
// The other direction, and the same principle: a function is inlined, so it
// runs in the caller's build environment and reads what is there. Only ARGs are
// scoped - they are the function's interface, and one that silently saw its
// caller's arguments would behave differently depending on where it was called
// from.
//
// Found immediately after fixing the outward direction. `earthly-lib`'s rust
// library calls `+INIT` to set `EARTHLY_CACHE_PREFIX` and then, inside another
// function, runs:
//
//	RUN if [ ! -n "$EARTHLY_CACHE_PREFIX" ]; then echo "+INIT has not been
//	    called yet in this build environment"; exit 1; fi
//
// which is a library telling a user their build is misconfigured, because the
// variable its own `+INIT` set was not visible one call deeper.
func TestAFunctionSeesTheCallersEnvironment(t *testing.T) {
	t.Parallel()

	src := `VERSION 0.8

CHECK:
    FUNCTION
    RUN echo $GREETING

probe:
    FROM alpine:3.22
    ENV GREETING=hello
    DO +CHECK
`

	plan, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
	if err != nil {
		t.Fatalf("%v", err)
	}

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && n.Op.Env["GREETING"] == testGreeting {
			return
		}
	}

	t.Error("a step inside the function does not carry the environment the caller set")
}
