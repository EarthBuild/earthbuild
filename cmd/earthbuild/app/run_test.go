package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactSecretsFromArgs(t *testing.T) {
	for _, testCase := range []struct {
		args     []string
		expected []string
	}{
		{
			args:     []string{"earthbuild", "--secret", "foo=bar"},
			expected: []string{"earthbuild", "--secret", "foo=XXXXX"},
		},
		{
			args:     []string{"earthbuild", "--secret", "foo=bar", "--ci"},
			expected: []string{"earthbuild", "--secret", "foo=XXXXX", "--ci"},
		},
		{
			args:     []string{"earthbuild", "--secret", "foo", "--ci"},
			expected: []string{"earthbuild", "--secret", "foo", "--ci"},
		},
		{
			args:     []string{"earthbuild", "-s", "foo=bar"},
			expected: []string{"earthbuild", "-s", "foo=XXXXX"},
		},
		{
			args:     []string{"earthbuild", "-s", "foo=bar", "--ci"},
			expected: []string{"earthbuild", "-s", "foo=XXXXX", "--ci"},
		},
		{
			args:     []string{"earthbuild", "-s", "foo", "--ci"},
			expected: []string{"earthbuild", "-s", "foo", "--ci"},
		},
	} {
		actual := redactSecretsFromArgs(testCase.args)
		require.ElementsMatch(t, testCase.expected, actual)
	}
}
