package subcmd

import (
	"context"

	"github.com/EarthBuild/earthbuild/cmd/earthly/flag"
	"github.com/EarthBuild/earthbuild/config"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logbus/setup"
	"github.com/moby/buildkit/client"
	"github.com/urfave/cli/v3"
)

type CLI interface {
	App() *cli.Command

	Version() string
	GitSHA() string

	Flags() *flag.Global
	Console() conslogging.ConsoleLogger
	SetConsole(conslogging.ConsoleLogger)

	InitFrontend(context.Context, *cli.Command) error
	Cfg() *config.Config
	SetCommandName(name string)

	GetBuildkitClient(context.Context, *cli.Command) (client *client.Client, err error)

	LogbusSetup() *setup.BusSetup
	Logbus() *logbus.Bus

	AddDeferredFunc(f func())
}
