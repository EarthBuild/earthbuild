package subcmd_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/EarthBuild/earthbuild/cmd/earthly/app"
	"github.com/EarthBuild/earthbuild/cmd/earthly/base"
	"github.com/EarthBuild/earthbuild/cmd/earthly/subcmd"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/poy/onpar"
	"github.com/poy/onpar/expect"
	"github.com/poy/onpar/matchers"
	"github.com/urfave/cli/v2"
)

func TestRootCmdsHelp(t *testing.T) {
	t.Parallel()

	type testCtx struct {
		t      *testing.T
		expect expect.Expectation
	}

	o := onpar.New()

	o.BeforeEach(func(t *testing.T) testCtx {
		t.Helper()

		return testCtx{
			t:      t,
			expect: expect.New(t),
		}
	})
	defer o.Run(t)

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

	for _, cmd := range checkSubCommands(rootCLI) {
		o.Spec(fmt.Sprintf("Help usage for %s should not end with '.'", cmd.Name), func(tt testCtx) {
			tt.expect(cmd.Usage).To(matchers.Not(matchers.EndWith(".")))
		})
		o.Spec(fmt.Sprintf("Help description for %s should end with '.'", cmd.Name), func(tt testCtx) {
			tt.expect(cmd.Description).To(matchers.EndWith("."))
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
