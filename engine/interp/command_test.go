package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `COMMAND` is what `FUNCTION` was called before it was renamed.
//
// The parser knows both and keeps them apart, which is right - a diagnostic
// should quote the word the author wrote. The interpreter only knew one, so an
// Earthfile using the older spelling was refused as an unsupported construct
// rather than run. They declare the same thing: that a block is a function
// rather than a target.
//
// **Each in its own dialect.** This ran both under one VERSION line until the
// corpus said each version has exactly one spelling and refuses the other
// (E459) - so what it asserts now is that the older word means what the newer
// one means, which is what it was always for.
func TestCommandIsFunctionsOlderName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ word, version string }{
		{"FUNCTION", "VERSION 0.8\n"},
		{"COMMAND", "VERSION 0.7\n"},
	} {
		word := tc.word

		t.Run(word, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(tc.version+`
main:
    FROM alpine:3.22
    DO +GREET --name=world

GREET:
    `+word+`
    ARG name
    RUN hello-$name
`, testMain)
			if err != nil {
				t.Fatal(err)
			}

			if got := describe(p.Graph.Nodes()); !strings.Contains(got, "hello-world") {
				t.Errorf("the function did not run:\n%s", got)
			}
		})
	}
}
