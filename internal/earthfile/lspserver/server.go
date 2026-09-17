package lspserver

import (
	"context"
	"io"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/server"
)

// Run serves Earthfile LSP requests over rw until the client exits or ctx is
// cancelled.
func Run(ctx context.Context, version string, rw io.ReadWriteCloser) error {
	handler := NewHandler(version)
	srv := server.NewServer(
		handler,
		server.WithSemanticTokensOptions(semanticTokensOptions()),
		server.WithCompletionOptions(completionOptions()),
	)

	return srv.Run(ctx, rw)
}

// completionTriggerCharacters are the characters that begin an Earthfile
// completion context without being part of the word being typed.
var completionTriggerCharacters = []string{"+", "-", "/"}

func completionOptions() lsp.CompletionOptions {
	return lsp.CompletionOptions{TriggerCharacters: completionTriggerCharacters}
}

func semanticTokensOptions() lsp.SemanticTokensOptions {
	return lsp.SemanticTokensOptions{
		Legend: lsp.SemanticTokensLegend{
			TokenTypes: semanticTokenTypes,
			TokenModifiers: []string{
				"declaration",
			},
		},
	}
}
