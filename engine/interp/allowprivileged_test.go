package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestPrivilegedIsAllowedWhenTheCallerOptsIn.
//
// **`RUN --privileged` is refused by default and that is right**: a step here
// already holds every capability inside its namespace and cannot reach past it,
// so the flag promises something it cannot deliver and the refusal says so.
//
// It is refused *by default*, though, not for ever. `--allow-privileged` is the
// caller saying they know what the flag does here and want it anyway - sixteen
// of this repository's own corpus invocations pass it - and an engine that
// refuses a construct the operator has explicitly opted into is refusing to be
// used rather than refusing to be wrong.
func TestPrivilegedIsAllowedWhenTheCallerOptsIn(t *testing.T) {
	t.Parallel()

	const src = `VERSION 0.8
t:
    FROM alpine:3.21
    RUN --privileged echo hi
`

	_, err := interp.Build(src, "t")
	if err == nil {
		t.Fatal("RUN --privileged was accepted with nobody asking for it")
	}

	if !strings.Contains(err.Error(), "--privileged") {
		t.Errorf("the refusal does not name the flag: %v", err)
	}

	_, err = interp.Build(src, "t", interp.WithAllowPrivileged(true))
	if err != nil {
		t.Errorf("RUN --privileged refused although the caller opted in: %v", err)
	}
}
