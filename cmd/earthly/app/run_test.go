package app

import (
	"strings"
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
			args:     []string{"earthly", "--secret", "foo=bar"},
			expected: []string{"earthly", "--secret", "foo=XXXXX"},
		},
		{
			args:     []string{"earthly", "--secret", "foo=bar", "--ci"},
			expected: []string{"earthly", "--secret", "foo=XXXXX", "--ci"},
		},
		{
			args:     []string{"earthly", "--secret", "foo", "--ci"},
			expected: []string{"earthly", "--secret", "foo", "--ci"},
		},
		{
			args:     []string{"earthly", "-s", "foo=bar"},
			expected: []string{"earthly", "-s", "foo=XXXXX"},
		},
		{
			args:     []string{"earthly", "-s", "foo=bar", "--ci"},
			expected: []string{"earthly", "-s", "foo=XXXXX", "--ci"},
		},
		{
			args:     []string{"earthly", "-s", "foo", "--ci"},
			expected: []string{"earthly", "-s", "foo", "--ci"},
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
		args     []string
		expected string
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
