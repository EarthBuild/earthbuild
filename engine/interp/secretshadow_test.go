package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestASecretIsNotExpandedAwayByABuildArgument.
//
// `tests/secrets-args-precedence.earth` is six lines and the whole of the rule:
//
//	ARG foo = bacon
//	RUN --secret foo test "$foo" == "eggs"
//
// driven with `--secret foo=eggs`. Inside that RUN, `$foo` is the *secret*.
// The build argument of the same name is shadowed for the length of the
// command, which is what `--secret foo` asks for.
//
// This engine expanded `$foo` from the build arguments before the step ran, so
// the shell was handed `test "bacon" == "eggs"` and the secret it was given
// never had a chance to be read. **A secret silently replaced by a build
// argument is the failure worth naming**: the value is wrong, plausible, and
// arrives without any complaint.
//
// A name the RUN does not declare as a secret is expanded as before - that is
// how every other argument reaches a command.
func TestASecretIsNotExpandedAwayByABuildArgument(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    ARG foo = bacon
    ARG bar = chips
    RUN --secret foo test "$foo" = "$bar"
`, testMain, interp.WithSecrets(map[string]string{"foo": "eggs"}))
	if err != nil {
		t.Fatal(err)
	}

	got := describe(p.Graph.Nodes())

	if !strings.Contains(got, `"$foo"`) {
		t.Errorf("the step runs %q; `$foo` is a secret here and must reach the"+
			" shell, which is the only thing that has its value", got)
	}

	// And a name that is not a secret is still expanded here.
	if !strings.Contains(got, `"chips"`) {
		t.Errorf("the step runs %q; `$bar` is an ordinary argument", got)
	}
}
