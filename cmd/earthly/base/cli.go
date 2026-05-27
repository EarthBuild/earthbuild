package base

import (
	"github.com/EarthBuild/earthbuild/buildkitd"
	"github.com/EarthBuild/earthbuild/cmd/earthly/flag"
	"github.com/EarthBuild/earthbuild/config"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logbus/setup"
	"github.com/urfave/cli/v3"
)

// CLI contains the common "earth" command line interface.
type CLI struct {
	commandName             string
	version                 string
	gitSHA                  string
	builtBy                 string
	defaultBuildkitdImage   string
	defaultInstallationName string
	deferredFuncs           []func()
	app                     *cli.Command
	cfg                     *config.Config
	logbusSetup             *setup.BusSetup
	logbus                  *logbus.Bus
	console                 conslogging.ConsoleLogger
	flags                   flag.Global
}

// CLIOpt is a functional option for configuring a CLI.
type CLIOpt func(CLI) CLI

// WithVersion sets the version of the CLI.
func WithVersion(version string) CLIOpt {
	return func(c CLI) CLI {
		c.version = version
		return c
	}
}

// WithGitSHA sets the git sha of the CLI.
func WithGitSHA(sha string) CLIOpt {
	return func(c CLI) CLI {
		c.gitSHA = sha
		return c
	}
}

// WithBuiltBy sets the built by string of the CLI.
func WithBuiltBy(builtby string) CLIOpt {
	return func(c CLI) CLI {
		c.builtBy = builtby
		return c
	}
}

// WithDefaultBuildkitdImage sets the default buildkitd image of the CLI.
func WithDefaultBuildkitdImage(image string) CLIOpt {
	return func(c CLI) CLI {
		c.defaultBuildkitdImage = image
		return c
	}
}

// WithDefaultInstallationName sets the default installation name of the CLI.
func WithDefaultInstallationName(name string) CLIOpt {
	return func(c CLI) CLI {
		c.defaultInstallationName = name
		return c
	}
}

// NewCLI creates a new [CLI].
func NewCLI(console conslogging.ConsoleLogger, opts ...CLIOpt) *CLI {
	cli := CLI{
		app:     new(cli.Command),
		console: console,
		logbus:  logbus.New(),
		flags: flag.Global{
			BuildkitdSettings: buildkitd.Settings{},
		},
	}

	for _, opt := range opts {
		cli = opt(cli)
	}

	return &cli
}

// App returns the app's [cli.Command].
func (c *CLI) App() *cli.Command {
	return c.app
}

// SetAppUsage sets the usage text of the app.
func (c *CLI) SetAppUsage(usage string) {
	c.app.Usage = usage
}

// SetAppUsageText sets the extended usage text of the app.
func (c *CLI) SetAppUsageText(usageText string) {
	c.app.UsageText = usageText
}

// SetAppUseShortOptionHandling sets whether the app should use short option handling.
func (c *CLI) SetAppUseShortOptionHandling(use bool) {
	c.app.UseShortOptionHandling = use
}

// SetAppStopOnNthArg tells the cli that it should stop processing flags after the Nth argument.
func (c *CLI) SetAppStopOnNthArg(stop *int) {
	c.app.StopOnNthArg = stop
}

// SetAction sets the action of the app.
func (c *CLI) SetAction(action cli.ActionFunc) {
	c.app.Action = action
}

// SetVersion sets the version of the app.
func (c *CLI) SetVersion(version string) {
	c.app.Version = version
}

// SetFlags sets the flags of the app.
func (c *CLI) SetFlags(flags []cli.Flag) {
	c.app.Flags = flags
}

// SetCommands sets the subcommands of the app.
func (c *CLI) SetCommands(commands []*cli.Command) {
	c.app.Commands = commands
}

// SetBefore sets the before function of the app.
func (c *CLI) SetBefore(before cli.BeforeFunc) {
	c.app.Before = before
}

// Console returns the console logger.
func (c *CLI) Console() conslogging.ConsoleLogger {
	return c.console
}

// SetConsole sets the console logger.
func (c *CLI) SetConsole(cons conslogging.ConsoleLogger) {
	c.console = cons
}

// Cfg returns the config.
func (c *CLI) Cfg() *config.Config {
	return c.cfg
}

// SetCfg sets the config.
func (c *CLI) SetCfg(cfg *config.Config) {
	c.cfg = cfg
}

// CommandName returns the command name.
func (c *CLI) CommandName() string {
	return c.commandName
}

// SetCommandName sets the command name.
func (c *CLI) SetCommandName(commandName string) {
	c.commandName = commandName
}

// Version returns the version.
func (c *CLI) Version() string {
	return c.version
}

// GitSHA returns the git sha.
func (c *CLI) GitSHA() string {
	return c.gitSHA
}

// BuiltBy returns the "built by" string.
func (c *CLI) BuiltBy() string {
	return c.builtBy
}

// DefaultBuildkitdImage returns the default buildkitd image.
func (c *CLI) DefaultBuildkitdImage() string {
	return c.defaultBuildkitdImage
}

// DefaultInstallationName returns the default installation name.
func (c *CLI) DefaultInstallationName() string {
	return c.defaultInstallationName
}

// LogbusSetup returns the logbus setup.
func (c *CLI) LogbusSetup() *setup.BusSetup {
	return c.logbusSetup
}

// SetLogbusSetup sets the logbus setup.
func (c *CLI) SetLogbusSetup(setup *setup.BusSetup) {
	c.logbusSetup = setup
}

// Logbus returns the logbus.
func (c *CLI) Logbus() *logbus.Bus {
	return c.logbus
}

// SetLogbus sets the logbus.
func (c *CLI) SetLogbus(logbus *logbus.Bus) {
	c.logbus = logbus
}

// Flags returns the global flags.
func (c *CLI) Flags() *flag.Global {
	return &c.flags
}

// AddDeferredFunc adds a function to be executed after the app is run.
func (c *CLI) AddDeferredFunc(f func()) {
	c.deferredFuncs = append([]func(){f}, c.deferredFuncs...)
}

// ExecuteDeferredFuncs executes all deferred functions.
func (c *CLI) ExecuteDeferredFuncs() {
	for _, f := range c.deferredFuncs {
		f()
	}
}
