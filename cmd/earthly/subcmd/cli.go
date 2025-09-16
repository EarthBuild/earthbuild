package subcmd

import (
	"github.com/moby/buildkit/client"
	"github.com/urfave/cli/v2"

	"github.com/EarthBuild/earthbuild/cmd/earthly/flag"
	"github.com/EarthBuild/earthbuild/config"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logbus/setup"
)

type CLI interface {
	App() *cli.App

	Version() string
	GitSHA() string

	Flags() *flag.Global
	Console() conslogging.ConsoleLogger
	SetConsole(conslogging.ConsoleLogger)

	InitFrontend(*cli.Context) error
	Cfg() *config.Config
	SetCommandName(name string)

	GetBuildkitClient(*cli.Context) (client *client.Client, err error)

	LogbusSetup() *setup.BusSetup
	Logbus() *logbus.Bus

	AddDeferredFunc(f func())
}
