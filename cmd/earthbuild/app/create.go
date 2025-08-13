package app

import (
	"context"
	"fmt"

	"github.com/earthbuild/earthbuild/cmd/earthbuild/base"
	"github.com/earthbuild/earthbuild/cmd/earthbuild/common"
	"github.com/earthbuild/earthbuild/cmd/earthbuild/subcmd"
)

type earthbuildApp struct {
	BaseCLI *base.CLI
}

func NewearthbuildApp(cliInstance *base.CLI, rootApp *subcmd.Root, buildApp *subcmd.Build, ctx context.Context) *earthbuildApp {
	earthbuild := common.GetBinaryName()
	earthbuildApp := &earthbuildApp{BaseCLI: cliInstance}

	earthbuildApp.BaseCLI.SetAppUsage("The CI/CD framework that runs anywhere!")
	earthbuildApp.BaseCLI.SetAppUsageText("\t" + earthbuild + " [options] <target-ref>\n" +
		"   \t" + earthbuild + " [options] --image <target-ref>\n" +
		"   \t" + earthbuild + " [options] --artifact <target-ref>/<artifact-path> [<dest-path>]\n" +
		"   \t" + earthbuild + " [options] command [command options]\n" +
		"\n" +
		"Executes earthbuild builds. For more information see https://docs.earthbuild.dev/docs/earthbuild-command.\n" +
		"To get started with using earthbuild check out the getting started guide at https://docs.earthbuild.dev/basics.\n" +
		"\n" +
		"For help on build-specific flags try \n" +
		"\n" +
		"\t" + earthbuild + " build --help")
	earthbuildApp.BaseCLI.SetAppUseShortOptionHandling(true)
	earthbuildApp.BaseCLI.SetAction(buildApp.Action)
	earthbuildApp.BaseCLI.SetVersion(getVersionPlatform(earthbuildApp.BaseCLI.Version(), earthbuildApp.BaseCLI.GitSHA(), earthbuildApp.BaseCLI.BuiltBy()))

	earthbuildApp.BaseCLI.SetFlags(cliInstance.Flags().RootFlags(cliInstance.DefaultInstallationName(), cliInstance.DefaultBuildkitdImage()))
	earthbuildApp.BaseCLI.SetFlags(append(earthbuildApp.BaseCLI.App().Flags, buildApp.HiddenFlags()...))

	earthbuildApp.BaseCLI.SetCommands(rootApp.Cmds())

	earthbuildApp.BaseCLI.SetBefore(earthbuildApp.before)

	return earthbuildApp
}

func getVersionPlatform(version string, gitSHA string, builtBy string) string {
	s := fmt.Sprintf("%s %s %s", version, gitSHA, common.GetPlatform())
	if builtBy != "" {
		s += " " + builtBy
	}
	return s
}
