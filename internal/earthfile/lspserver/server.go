package lspserver

import (
	"context"
	"io"

	"github.com/owenrumney/go-lsp/server"
)

// Run serves Earthfile LSP requests over rw until the client exits or ctx is
// cancelled.
func Run(ctx context.Context, version string, rw io.ReadWriteCloser) error {
	handler := NewHandler(version)
	return server.NewServer(handler).Run(ctx, rw)
}
