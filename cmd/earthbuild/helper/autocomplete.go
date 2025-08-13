package helper

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"

	"github.com/earthbuild/earthbuild/cmd/earthbuild/base"

	"github.com/earthbuild/earthbuild/autocomplete"
	"github.com/earthbuild/earthbuild/buildcontext"
	"github.com/earthbuild/earthbuild/conslogging"
	"github.com/earthbuild/earthbuild/util/cliutil"
)

// to enable autocomplete, enter
// complete -o nospace -C "/path/to/earthbuild" earthbuild
//
// alternatively, you can run earthbuild with COMP_LINE and COMP_POINT set; for example:
// COMP_LINE="earthbuild ./buildkitd+buildkitd --" COMP_POINT="32" ./build/linux/amd64/earthbuild
// COMP_LINE="earthbuild ~/test/simple+test -" COMP_POINT="28" ./build/linux/amd64/earthbuild
func AutoComplete(ctx context.Context, cli *base.CLI) {
	_, found := os.LookupEnv("COMP_LINE")
	if !found {
		return
	}

	_, debugEnabled := os.LookupEnv("EARTHBUILD_AUTOCOMPLETE_DEBUG")
	if debugEnabled {
		logDir, err := cliutil.GetOrCreateEarthbuildDir(cli.Flags().InstallationName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "GetOrCreateEarthbuildDir failed: %v\n", err)
			os.Exit(1)
		}
		logFile := filepath.Join(logDir, "autocomplete.log")
		err = os.MkdirAll(logDir, 0755)
		if err != nil {
			fmt.Fprintf(os.Stderr, "MkdirAll %s failed: %v\n", logDir, err)
			os.Exit(1)
		}
		autocomplete.SetupLog(logFile)
		autocomplete.Logf("COMP_LINE=%q COMP_POINT=%q", os.Getenv("COMP_LINE"), os.Getenv("COMP_POINT"))
	}

	cli.SetConsole(cli.Console().WithLogLevel(conslogging.Silent))

	err := autoCompleteImp(ctx, cli)
	if err != nil {
		autocomplete.Logf("error during autocomplete: %s", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func autoCompleteImp(ctx context.Context, cli *base.CLI) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Errorf("recovered panic in autocomplete %s: %s", r, debug.Stack())
		}
	}()

	compLine := os.Getenv("COMP_LINE")   // full command line
	compPoint := os.Getenv("COMP_POINT") // where the cursor is

	compPointInt, err := strconv.ParseUint(compPoint, 10, 64)
	if err != nil {
		return err
	}
	if !(compPointInt > 0 && compPointInt < math.MaxInt) {
		err = errors.Errorf("compPointInt is out of bounds.")
		return err
	}

	gitLookup := buildcontext.NewGitLookup(cli.Console(), cli.Flags().SSHAuthSock)
	resolver := buildcontext.NewResolver(nil, gitLookup, cli.Console(), "", cli.Flags().GitBranchOverride, "", 0, "")
	var gwClient gwclient.Client // TODO this is a nil pointer which causes a panic if we try to expand a remotely referenced earthfile
	// it's expensive to create this gwclient, so we need to implement a lazy eval which returns it when required.

	potentials, err := autocomplete.GetPotentials(ctx, resolver, gwClient, compLine, int(compPointInt), cli.App())
	if err != nil {
		return err
	}
	for _, p := range potentials {
		fmt.Printf("%s\n", p)
	}

	return nil
}
