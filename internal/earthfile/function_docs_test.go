package earthfile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePreservesFunctionDocs(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"legacy":   "# installs the tool.\nINSTALL:\n    FUNCTION\n    RUN true\n",
		"explicit": "# installs the tool.\nFUNCTION INSTALL:\n    RUN true\n",
	}

	for name, text := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree, err := Parse("Earthfile", text)
			require.NoError(t, err)
			require.Len(t, tree.Functions, 1)
			require.Equal(t, "installs the tool.\n", tree.Functions[0].Docs)
		})
	}
}
