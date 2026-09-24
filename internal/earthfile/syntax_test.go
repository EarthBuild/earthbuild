package earthfile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyntaxTokensUseCanonicalLexer(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\n# docs\nbuild:\n    LET URL=\"https://example.test\"\n    BUILD +other --FLAG=$VALUE\n"
	tokens := SyntaxTokens("Earthfile", text)

	got := make([]string, 0, len(tokens))
	for _, token := range tokens {
		got = append(got, text[token.Start:token.End])
	}

	require.Contains(t, got, "VERSION")
	require.Contains(t, got, "# docs")
	require.Contains(t, got, "LET")
	require.Contains(t, got, "\"https://example.test\"")
	require.Contains(t, got, "BUILD")
	require.Contains(t, got, "--FLAG")
	require.Contains(t, got, "VALUE")
}

func TestSyntaxTokensStopCleanlyAtLexicalError(t *testing.T) {
	t.Parallel()

	tokens := SyntaxTokens("Earthfile", "VERSION 0.8\nbuild:\n    RUN true\n    INVALID\n")
	require.NotEmpty(t, tokens)
}
