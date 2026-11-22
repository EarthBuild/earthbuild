package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestASecretReferenceNamesTheSecretSupplied.
//
// `RUN --secret=SECRET1=+secrets/SECRET1` is how the corpus writes a secret
// that lives in a project's secret store, and `--secret SECRET1=foo` on the
// command line is how a build supplies one without that store. They are the
// same secret under two spellings, and this engine matched only the second:
// `tests/secrets.earth` supplies SECRET1 and then asks for `+secrets/SECRET1`,
// and was told the secret "+secrets/SECRET1" was not supplied.
//
// The prefix names *where* a secret lives, not what it is called. An engine
// with no project store still has the value the caller passed, and refusing it
// over the spelling is a refusal about nothing.
func TestASecretReferenceNamesTheSecretSupplied(t *testing.T) {
	t.Parallel()

	const src = `
main:
    FROM alpine:3.22
    RUN --secret=TOKEN=+secrets/TOKEN use-it
`

	_, err := interp.Build(versioned+src, testMain,
		interp.WithSecrets(map[string]string{"TOKEN": "value"}))
	if err != nil {
		t.Errorf("a supplied secret did not satisfy `+secrets/` naming it: %v", err)
	}

	// A path under the prefix is a name too: `+secrets/foo/bar` is the secret
	// `foo/bar`, which is how `project-secrets.earth+local-override` supplies
	// it - `--secret foo/bar=override`.
	_, err = interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN --secret=T=+secrets/foo/bar use-it
`, testMain, interp.WithSecrets(map[string]string{"foo/bar": "override"}))
	if err != nil {
		t.Errorf("a pathed secret name was not matched: %v", err)
	}

	// **An empty source supplies nothing, and that is allowed.**
	// `ARG SECRET_ID=+secrets/SECRET1` overridden with `--build-arg SECRET_ID=""`
	// makes `RUN --secret=SECRET1=$SECRET_ID` name no secret at all, and
	// `tests/secrets.earth` asserts the variable is then empty *and the build
	// carries on*. Refusing it demands a secret the author deliberately
	// removed.
	_, err = interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG ID=
    RUN --secret=TOKEN=$ID test -z "$TOKEN"
`, testMain)
	if err != nil {
		t.Errorf("an empty secret source was refused: %v", err)
	}

	// A secret *mount* names one the same way, and reaches a different line.
	_, err = interp.Build(versioned+`
main:
    FROM alpine:3.22
    RUN --mount=type=secret,id=+secrets/TOKEN,target=/t use-it
`, testMain, interp.WithSecrets(map[string]string{"TOKEN": "value"}))
	if err != nil {
		t.Errorf("a mounted secret did not match the name supplied: %v", err)
	}

	// And one nobody supplied is still refused, by the name the caller has to
	// give - not by the spelling the Earthfile used.
	_, err = interp.Build(versioned+src, testMain)
	if err == nil {
		t.Fatal("a secret nobody supplied was accepted")
	}

	if !strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("refused with %q, which does not name the secret to supply", err)
	}
}
