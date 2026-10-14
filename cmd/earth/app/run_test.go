package app

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactSecretsFromArgs(t *testing.T) {
	t.Parallel()

	//nolint:goconst
	for _, testCase := range []struct {
		args     []string
		expected []string
	}{
		{
			args:     []string{"earth", "--secret", "foo=bar"},
			expected: []string{"earth", "--secret", "foo=XXXXX"},
		},
		{
			args:     []string{"earth", "--secret", "foo=bar", "--ci"},
			expected: []string{"earth", "--secret", "foo=XXXXX", "--ci"},
		},
		{
			args:     []string{"earth", "--secret", "foo", "--ci"},
			expected: []string{"earth", "--secret", "foo", "--ci"},
		},
		{
			args:     []string{"earth", "-s", "foo=bar"},
			expected: []string{"earth", "-s", "foo=XXXXX"},
		},
		{
			args:     []string{"earth", "-s", "foo=bar", "--ci"},
			expected: []string{"earth", "-s", "foo=XXXXX", "--ci"},
		},
		{
			args:     []string{"earth", "-s", "foo", "--ci"},
			expected: []string{"earth", "-s", "foo", "--ci"},
		},
	} {
		actual := redactSecretsFromArgs(testCase.args)
		require.ElementsMatch(t, testCase.expected, actual)
	}
}

func TestFirstTargetArg(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		want string
		args []string
	}{
		{name: "single target", args: []string{"earth", "+target"}, want: "+target"},
		{name: "flags before target", args: []string{"earth", "--verbose", "+build"}, want: "+build"},
		{name: "flags after target", args: []string{"earth", "+test", "--ci"}, want: "+test"},
		{name: "first of multiple targets", args: []string{"earth", "+first", "+second"}, want: "+first"},
		{name: "remote target", args: []string{"earth", "github.com/foo/bar+baz"}, want: ""},
		{name: "no target", args: []string{"earth", "--version"}, want: ""},
		{name: "empty args", args: []string{}, want: ""},
		{name: "only program name", args: []string{"earth"}, want: ""},
		{name: "skips non-targets", args: []string{"earth", "not-a-target", "+actual"}, want: "+actual"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, testCase.want, firstTargetArg(testCase.args))
		})
	}
}

func TestPrivilegedModeMessages(t *testing.T) {
	t.Parallel()

	missingFlagErr := errors.New(
		"build target: build main: unlazy force execution: failed to load LLB: security.insecure is not allowed")
	daemonErr := errors.New(
		"granting entitlement security.insecure is not allowed by build daemon configuration")

	t.Run("missing flag with target", func(t *testing.T) {
		t.Parallel()

		errMsg, helpMsg := privilegedModeMessages(missingFlagErr, []string{"earth", "+foo"})

		require.Equal(t, "This build requires privileged mode.", errMsg)
		require.Contains(t, helpMsg, "earth -P +foo")
		require.Contains(t, helpMsg, "EARTH_ALLOW_PRIVILEGED=true")
		require.NotContains(t, helpMsg, "allow_privileged", "there is no such config option")
	})

	t.Run("missing flag without target", func(t *testing.T) {
		t.Parallel()

		_, helpMsg := privilegedModeMessages(missingFlagErr, []string{"earth"})

		require.Contains(t, helpMsg, "earth -P +your-target")
	})

	t.Run("daemon disallows entitlement", func(t *testing.T) {
		t.Parallel()

		errMsg, helpMsg := privilegedModeMessages(daemonErr, []string{"earth", "-P", "+foo"})

		require.Contains(t, errMsg, "daemon")
		require.Contains(t, helpMsg, "insecure-entitlements")
		require.NotContains(t, helpMsg, "earth -P +foo", "-P cannot fix a daemon-side restriction")
	})
}
