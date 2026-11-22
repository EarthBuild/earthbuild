package subcmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompletionCommandNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		baseName string
		expected []string
	}{
		{
			name:     "official binary registers earth",
			baseName: "earth",
			expected: []string{"earth"},
		},
		{
			name:     "legacy binary also registers the earth alias it symlinks",
			baseName: "earthly",
			expected: []string{"earthly", "earth"},
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
		bashCompleteEntry("/usr/local/bin/earth", "earth"),
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
`, zshCompleteEntry("/usr/local/bin/earth", "earth"))
}
