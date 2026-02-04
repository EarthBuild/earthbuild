package subcmd_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/urfave/cli/v2"

	"github.com/EarthBuild/earthbuild/cmd/earthly/app"
	"github.com/EarthBuild/earthbuild/cmd/earthly/base"
	"github.com/EarthBuild/earthbuild/cmd/earthly/subcmd"
)

func TestRootCmdsHelp(t *testing.T) {
	t.Parallel()

	ctx := context.TODO()
	newCLI := base.NewCLI(conslogging.ConsoleLogger{},
		base.WithVersion(""),
		base.WithGitSHA(""),
		base.WithBuiltBy(""),
		base.WithDefaultBuildkitdImage(""),
		base.WithDefaultInstallationName(""),
	)
	buildApp := subcmd.NewBuild(newCLI)
	rootApp := subcmd.NewRoot(newCLI, buildApp)
	app := app.NewEarthlyApp(newCLI, rootApp, buildApp, ctx)

	rootCLI := app.BaseCLI.App().Commands

	for _, subCmd := range checkSubCommands(rootCLI) {
		t.Run(fmt.Sprintf("Help usage for %s should not end with '.'", subCmd.Name), func(t *testing.T) {
			t.Parallel()
			if strings.HasSuffix(subCmd.Usage, ".") {
				t.Errorf("command %q usage should not end with '.', got: %q", subCmd.Name, subCmd.Usage)
			}
		})

		t.Run(fmt.Sprintf("Help description for %s should end with '.'", subCmd.Name), func(t *testing.T) {
			t.Parallel()
			if !strings.HasSuffix(subCmd.Description, ".") {
				t.Errorf("command %q description should end with '.', got: %q", subCmd.Name, subCmd.Description)
			}
		})
	}
}

// Check if command has any subCommands to verify.
func checkSubCommands(commands []*cli.Command) []*cli.Command {
	allCommands := make([]*cli.Command, 0, len(commands))

	for _, command := range commands {
		allCommands = append(allCommands, command)
		if len(command.Subcommands) != 0 {
			allCommands = append(allCommands, checkSubCommands(command.Subcommands)...)
		}
	}

	return allCommands
}
