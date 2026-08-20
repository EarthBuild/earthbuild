package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A construct given one word too many says so.
//
// **E358 found `WITH DOCKER` discarding what it could not parse**, so an author
// who wrote `--cache-id=with space` got a cache called `with` and no indication.
// The parser hands back what it did not understand; the question this sweep asks
// is which other constructs take that list and drop part of it.
//
// A construct that takes a *variable* number of words - `RUN`, `IF`, `COPY`,
// `SAVE ARTIFACT` - has no extra word to find, and is not here. These take a
// fixed count, and a word past it is something the author wrote that this engine
// did not do (I10, E359).
//
// `BUILD` is not here and was not cleared: it refuses `BUILD +other extra` for a
// different reason - the target does not exist - and a fixture that cannot tell
// the two apart would assert nothing. Left out rather than asserted loosely.
func TestAConstructGivenOneWordTooManySaysSo(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ what, line string }{
		{"FROM", "FROM alpine extra"},
		{"CACHE", "CACHE /one /two"},
		{"GIT CLONE", "GIT CLONE https://example.com/r.git /dst extra"},
	} {
		_, err := interp.Build(`
VERSION 0.8
build:
    FROM alpine
    `+c.line+`
    RUN true
`, "build")
		if err == nil {
			t.Errorf("%s: %q was accepted and the extra word discarded",
				c.what, c.line)

			continue
		}

		if !strings.Contains(err.Error(), "extra") &&
			!strings.Contains(err.Error(), "/two") {
			t.Logf("%s refuses, and not for the extra word: %v", c.what, err)
		}
	}
}
