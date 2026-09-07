package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestABuildArgumentValueIsUnquotedAndUnescaped.
//
// `tests/escape.earth` passes a filename with a `+` in it, escaped because a
// `+` starts a target reference:
//
//	BUILD --build-arg FILE="file-with-\+.txt" +test-copy-build-arg
//
// The value the target receives is `file-with-+.txt` - the quotes are the
// Earthfile's punctuation and the backslash is what stops the `+` being read
// as a reference. This engine passed both through, so the target looked for a
// file called `"file-with-\+.txt"` and reported it missing from the build
// context, naming a file nobody has.
//
// The same rule the rest of the interpreter has: a value this engine consumes
// has its quoting resolved. A build argument is consumed here.
func TestABuildArgumentValueIsUnquotedAndUnescaped(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
dep:
    FROM alpine:3.22
    ARG FILE=none
    RUN saw-$FILE

main:
    FROM alpine:3.22
    BUILD --build-arg FILE="file-with-\+.txt" +dep
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())
	if !strings.Contains(got, "saw-file-with-+.txt") {
		t.Errorf("the target received the value as written rather than as"+
			" meant:\n%s", got)
	}
}
