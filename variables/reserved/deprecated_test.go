package reserved

import (
	"strings"
	"testing"
)

func TestDeprecatedBuiltin(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name            string
		wantReplacement string
		wantDeprecated  bool
	}{
		{
			name:            EarthlyGitProjectName,
			wantReplacement: EarthGitProjectName,
			wantDeprecated:  true,
		},
		{
			name:            EarthlyVersion,
			wantReplacement: EarthVersion,
			wantDeprecated:  true,
		},
		{
			name:            EarthlyTargetTagDocker,
			wantReplacement: EarthTargetTagDocker,
			wantDeprecated:  true,
		},
		{
			// Already using the current prefix; not deprecated.
			name:           EarthGitProjectName,
			wantDeprecated: false,
		},
		{
			// A user-defined arg that merely starts with EARTHLY_ is not a
			// built-in, so it is not flagged.
			name:           "EARTHLY_NOT_A_BUILTIN",
			wantDeprecated: false,
		},
		{
			name:           "MY_ARG",
			wantDeprecated: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			replacement, deprecated := DeprecatedBuiltin(tc.name)
			Equal(t, tc.wantDeprecated, deprecated)
			Equal(t, tc.wantReplacement, replacement)
		})
	}
}

// TestEveryDeprecatedBuiltinHasReplacement ensures the EARTHLY_ -> EARTH_
// migration is complete: every EARTHLY_-prefixed built-in ARG must have a
// corresponding EARTH_-prefixed built-in ARG so we can always point users at a
// replacement.
func TestEveryDeprecatedBuiltinHasReplacement(t *testing.T) {
	t.Parallel()

	for name := range args {
		if !strings.HasPrefix(name, DeprecatedPrefix) {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			replacement, deprecated := DeprecatedBuiltin(name)
			True(t, deprecated)
			True(t, IsBuiltIn(replacement))
			Equal(t, Prefix+strings.TrimPrefix(name, DeprecatedPrefix), replacement)
		})
	}
}
