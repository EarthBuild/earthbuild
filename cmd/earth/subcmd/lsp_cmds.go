package subcmd

import (
	"context"

	"github.com/EarthBuild/earthbuild/internal/earthfile/lspserver"
	"github.com/owenrumney/go-lsp/server"
	"github.com/urfave/cli/v3"
)

// LSP encapsulates the language server command.
type LSP struct {
	cli CLI
}

// NewLSP creates the language server command.
func NewLSP(cli CLI) *LSP {
	return &LSP{cli: cli}
}

// Cmds returns the language server CLI command.
func (a *LSP) Cmds() []*cli.Command {
	return []*cli.Command{{
		Name:        "lsp",
		Usage:       "Run the Earthfile language server",
		UsageText:   "earth lsp",
		Description: "Run the Earthfile language server over standard input and output.",
		Action:      a.action,
	}}
}

func (a *LSP) action(ctx context.Context, _ *cli.Command) error {
	a.cli.SetCommandName("lsp")
	return lspserver.Run(ctx, a.cli.Version(), server.RunStdio())
}
