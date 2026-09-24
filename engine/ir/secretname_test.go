package ir_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A secret's name is the same however the Earthfile spelled it.
func TestSecretNameIgnoresWhereTheSecretLives(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ in, want string }{
		{"+secrets/TOKEN", "TOKEN"},
		{"TOKEN", "TOKEN"},
		// A path under the prefix is a name too: `project-secrets.earth` is
		// driven with `--secret foo/bar=override`.
		{"+secrets/foo/bar", "foo/bar"},
		// Only the prefix, and only at the front: a secret really called
		// `x+secrets/y` is not two secrets.
		{"x+secrets/y", "x+secrets/y"},
		{"", ""},
	} {
		if got := ir.SecretName(c.in); got != c.want {
			t.Errorf("SecretName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
