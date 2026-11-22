package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A quoted `--load` reference is a reference, not a name beginning with a quote.
//
// Found by auditing what the corpus is refused *for*. 102 of 478 targets are
// turned away as invalid input, nearly all of them correctly - an unsupplied
// `--required` ARG, a secret nobody passed - and one of them was this:
//
//	WITH DOCKER --load other-name:latest="(+a-test-image --name=bar --var buz)"
//	  "\"(" was never imported (Earthfile:92)
//	  add `IMPORT <path> AS "(`, or write the path directly as ./"(+a-test-image ...)"
//
// `loadSource` decides between the two forms a reference can take by asking
// whether it begins with `(`, and this one begins with `"`. So the parenthesised
// form was never entered, the whole string went to the target resolver, and
// `"(` came back out as an import alias - with advice to declare it, which is
// not a thing anybody can do.
//
// **This is green paper A6, which is in the specification because of this exact
// mistake made somewhere else**: the grammar defines a path as excluding quote
// characters unquoted and permitting a QUOTED-STRING otherwise, so quotes
// delimit a value and are not part of it. Treating them as part of it once
// produced a file-not-found for a file nobody has, 226 times in one repository.
//
// The sibling on the line above - quotes around the *whole* value rather than
// around the part after `=` - has always worked, because the parser strips
// those. Two spellings of one thing, one working, and the corpus is what
// noticed.
func TestAQuotedDockerLoadReferenceIsStillAReference(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		load string
	}{
		{
			name: "unquoted, with build arguments",
			load: `--load=(+img --name=bar)`,
		},
		{
			// The form that has always worked: the quotes wrap everything.
			name: "the whole value quoted",
			load: `--load="other:latest=(+img --name=bar)"`,
		},
		{
			// The form that did not.
			name: "only the reference quoted",
			load: `--load=other:latest="(+img --name=bar)"`,
		},
		{
			name: "only the reference quoted, no build arguments",
			load: `--load=other:latest="+img"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := `VERSION 0.8

img:
    FROM alpine:3.22
    ARG name=unset
    RUN echo $name > /n
    SAVE IMAGE other:latest

probe:
    FROM alpine:3.22
    WITH DOCKER ` + tc.load + `
        RUN docker run other:latest
    END
`

			_, err := interp.Build(src, "probe", interp.WithPlatform("linux/arm64"))
			if err == nil {
				return
			}

			// A refusal naming WITH DOCKER is this engine saying it cannot do
			// the construct, which is a different answer and an honest one.
			// What must not happen is a diagnosis about an import.
			if strings.Contains(err.Error(), "never imported") {
				t.Errorf("the reference was read as an import alias:\n%v", err)
			}

			if strings.Contains(err.Error(), `"(`) {
				t.Errorf("a quote character reached the diagnosis:\n%v", err)
			}
		})
	}
}
