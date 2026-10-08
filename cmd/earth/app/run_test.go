package app

import (
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
