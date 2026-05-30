package app

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/cmd/earthly/base"
	"github.com/EarthBuild/earthbuild/cmd/earthly/common"
	"github.com/EarthBuild/earthbuild/cmd/earthly/subcmd"
)

// EarthApp encapsulates the core earth command-line application.
type EarthApp struct {
	BaseCLI *base.CLI
}

// NewEarthApp creates a new [EarthApp].
func NewEarthApp(cliInstance *base.CLI, rootApp *subcmd.Root, buildApp *subcmd.Build) *EarthApp {
	earth := common.GetBinaryName()
	earthApp := &EarthApp{BaseCLI: cliInstance}

	earthApp.BaseCLI.SetAppUsage("The CI/CD framework that runs anywhere!")
	earthApp.BaseCLI.SetAppUsageText("\t" + earth + " [options] <target-ref>\n" +
		"   \t" + earth + " [options] --image <target-ref>\n" +
		"   \t" + earth + " [options] --artifact <target-ref>/<artifact-path> [<dest-path>]\n" +
		"   \t" + earth + " [options] command [command options]\n" +
		"\n" +
		"Executes earth builds. For more information see https://docs.earthbuild.dev/docs/earthly-command.\n" +
		"To get started with using earth check out the getting started guide at https://docs.earthbuild.dev/basics.\n" +
		"\n" +
		"For help on build-specific flags try \n" +
		"\n" +
		"\t" + earth + " build --help")
	earthApp.BaseCLI.SetAppUseShortOptionHandling(true)
	earthApp.BaseCLI.SetAppStopOnNthArg(new(1))
	earthApp.BaseCLI.SetAction(buildApp.Action)
	earthApp.BaseCLI.SetVersion(
		getVersionPlatform(earthApp.BaseCLI.Version(), earthApp.BaseCLI.GitSHA(), earthApp.BaseCLI.BuiltBy()))

	earthApp.BaseCLI.SetFlags(
		cliInstance.Flags().RootFlags(cliInstance.DefaultInstallationName(), cliInstance.DefaultBuildkitdImage()))
	earthApp.BaseCLI.SetFlags(append(earthApp.BaseCLI.App().Flags, buildApp.HiddenFlags()...))

	earthApp.BaseCLI.SetCommands(rootApp.Cmds())

	earthApp.BaseCLI.SetBefore(earthApp.before)

	return earthApp
}

func getVersionPlatform(version string, gitSHA string, builtBy string) string {
	s := fmt.Sprintf("%s %s %s", version, gitSHA, common.GetPlatform())
	if builtBy != "" {
		s += " " + builtBy
	}

	return s
}
