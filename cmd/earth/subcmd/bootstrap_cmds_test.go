package subcmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// earthCmd is the command word the official binary is installed under.
const earthCmd = "earth"

func TestCompletionCommandNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		baseName string
		expected []string
	}{
		{
			name:     "official binary registers earth",
			baseName: earthCmd,
			expected: []string{earthCmd},
		},
		{
			name:     "legacy binary also registers the earth alias it symlinks",
			baseName: "earthly",
			expected: []string{"earthly", earthCmd},
		},
		{
			name:     "custom binary name is used verbatim",
			baseName: "my-earth",
			expected: []string{"my-earth"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, completionCommandNames(tt.baseName))
		})
	}
}

func TestBashCompleteEntry(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		"complete -o nospace -C '/usr/local/bin/earth' earth\n",
		bashCompleteEntry("/usr/local/bin/earth", earthCmd),
	)
}

func TestZshCompleteEntry(t *testing.T) {
	t.Parallel()

	require.Equal(t, `#compdef _earth earth

function _earth {
    autoload -Uz bashcompinit
    bashcompinit
    complete -o nospace -C '/usr/local/bin/earth' earth
}
`, zshCompleteEntry("/usr/local/bin/earth", earthCmd))
}
