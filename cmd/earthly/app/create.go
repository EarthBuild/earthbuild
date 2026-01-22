package app

import (
	"context"
	"fmt"

	"github.com/EarthBuild/earthbuild/cmd/earthly/base"
	"github.com/EarthBuild/earthbuild/cmd/earthly/common"
	"github.com/EarthBuild/earthbuild/cmd/earthly/subcmd"
)

type EarthlyApp struct {
	BaseCLI *base.CLI
}

func NewEarthlyApp(
	cliInstance *base.CLI, rootApp *subcmd.Root, buildApp *subcmd.Build, ctx context.Context,
) *EarthlyApp {
	earthly := common.GetBinaryName()
	earthlyApp := &EarthlyApp{BaseCLI: cliInstance}

	earthlyApp.BaseCLI.SetAppUsage("The CI/CD framework that runs anywhere!")
	earthlyApp.BaseCLI.SetAppUsageText("\t" + earthly + " [options] <target-ref>\n" +
		"   \t" + earthly + " [options] --image <target-ref>\n" +
		"   \t" + earthly + " [options] --artifact <target-ref>/<artifact-path> [<dest-path>]\n" +
		"   \t" + earthly + " [options] command [command options]\n" +
		"\n" +
		"Executes earth builds. For more information see https://docs.earthbuild.dev/docs/earthly-command.\n" +
		"To get started with using EarthBuild check out the getting started guide at https://docs.earthbuild.dev/basics.\n" +
		"\n" +
		"For help on build-specific flags try \n" +
		"\n" +
		"\t" + earthly + " build --help")
	earthlyApp.BaseCLI.SetAppUseShortOptionHandling(true)
	earthlyApp.BaseCLI.SetAction(buildApp.Action)
	earthlyApp.BaseCLI.SetVersion(
		getVersionPlatform(earthlyApp.BaseCLI.Version(), earthlyApp.BaseCLI.GitSHA(), earthlyApp.BaseCLI.BuiltBy()))

	earthlyApp.BaseCLI.SetFlags(
		cliInstance.Flags().RootFlags(cliInstance.DefaultInstallationName(), cliInstance.DefaultBuildkitdImage()))
	earthlyApp.BaseCLI.SetFlags(append(earthlyApp.BaseCLI.App().Flags, buildApp.HiddenFlags()...))

	earthlyApp.BaseCLI.SetCommands(rootApp.Cmds())

	earthlyApp.BaseCLI.SetBefore(earthlyApp.before)

	return earthlyApp
}

func getVersionPlatform(version string, gitSHA string, builtBy string) string {
	s := fmt.Sprintf("%s %s %s", version, gitSHA, common.GetPlatform())
	if builtBy != "" {
		s += " " + builtBy
	}

	return s
}
