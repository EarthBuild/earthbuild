package app

import (
	"strings"
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

func TestExtractTargetFromArgs(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		expected string
		args     []string
	}{
		{
			name:     "single target",
			args:     []string{"earthly", "+target"},
			expected: "+target",
		},
		{
			name:     "target with flags before",
			args:     []string{"earthly", "--verbose", "+build"},
			expected: "+build",
		},
		{
			name:     "target with flags after",
			args:     []string{"earthly", "+test", "--ci"},
			expected: "+test",
		},
		{
			name:     "multiple targets - returns first",
			args:     []string{"earthly", "+first", "+second"},
			expected: "+first",
		},
		{
			name:     "no target",
			args:     []string{"earthly", "--version"},
			expected: "",
		},
		{
			name:     "empty args",
			args:     []string{},
			expected: "",
		},
		{
			name:     "only command",
			args:     []string{"earthly"},
			expected: "",
		},
		{
			name:     "target-like string but not target",
			args:     []string{"earthly", "not-a-target", "+actual-target"},
			expected: "+actual-target",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			// Simulate the target extraction logic from handleError
			var targetInfo string
			if len(testCase.args) > 1 {
				for _, arg := range testCase.args[1:] {
					if strings.HasPrefix(arg, "+") {
						targetInfo = arg
						break
					}
				}
			}

			require.Equal(t, testCase.expected, targetInfo)
		})
	}
}
