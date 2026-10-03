package earthfile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceTokensPreserveParameterizedArtifact(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\ncopy:\n    COPY (monorepo+compiled-code/packages --scope=\"server\") ./packages\n"
	tokens, err := SourceTokens("Earthfile", text)
	require.NoError(t, err)

	var argument SourceToken

	for _, token := range tokens {
		if token.Kind == SourceTokenArgument &&
			token.Text == "(monorepo+compiled-code/packages" {
			argument = token
			break
		}
	}

	require.Equal(t, "(monorepo+compiled-code/packages", argument.Text)
	require.Equal(t, argument.Text, text[argument.Start:argument.End])
	require.Equal(t, 3, argument.Line)
	require.Equal(t, 10, argument.Column)
}

func TestSourceTokensReturnLosslessPrefixBeforeError(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\nbuild:\n    RUN \"unterminated\n"
	tokens, err := SourceTokens("Earthfile", text)
	require.Error(t, err)
	require.NotEmpty(t, tokens)

	foundRun := false

	for _, token := range tokens {
		foundRun = foundRun || token.Kind == SourceTokenKeyword && token.Text == string(CmdRun)
	}

	require.True(t, foundRun)
}
