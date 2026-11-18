package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactSecretsFromArgs(t *testing.T) {
	t.Parallel()

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

func TestRunExitCodeRegex(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		errStr string
		want   bool
	}{
		{name: "non-zero exit", errStr: `process "/bin/sh -c false" did not complete successfully: exit code: 1`, want: true},
		{name: "zero exit", errStr: `process "/bin/sh -c true" did not complete successfully: exit code: 0`, want: false},
		{
			name:   "with target suffix",
			errStr: "process \"/bin/sh -c false\" did not complete successfully: exit code: 2\n\tin\t+target",
			want:   true,
		},
		// buildkit v0.16+ replaces the exit code with ENOMEM when the cgroup's oom_kill count is set.
		{
			name:   "oom killed",
			errStr: `process "/bin/sh -c x" did not complete successfully: cannot allocate memory`,
			want:   true,
		},
		{name: "unrelated", errStr: "failed to solve: some other error", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, runExitCodeRegex.MatchString(tc.errStr))
		})
	}
}
