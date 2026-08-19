package subcmd

import (
	"github.com/urfave/cli/v3"
)

// Root encapsulates the root command logic.
type Root struct {
	cli CLI

	buildCmd *Build
}

// NewRoot creates a new Root command.
func NewRoot(cli CLI, buildCmd *Build) *Root {
	return &Root{
		cli:      cli,
		buildCmd: buildCmd,
	}
}

// Cmds returns the list of commands for the root command.
func (a *Root) Cmds() []*cli.Command {
	cmds := concatCmds([][]*cli.Command{
		NewDebug(a.cli).Cmds(),
		NewBootstrap(a.cli).Cmds(),
		a.buildCmd.Cmds(),
		NewConfig(a.cli).Cmds(),
		NewDoc(a.cli).Cmds(),
		NewDoc2Earth(a.cli).Cmds(),
		NewInit(a.cli).Cmds(),
		NewList(a.cli).Cmds(),
		NewPrune(a.cli).Cmds(),
	})

	return cmds
}
