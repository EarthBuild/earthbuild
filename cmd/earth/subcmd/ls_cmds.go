package subcmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/urfave/cli/v3"
)

// List encapsulates the ls command logic.
type List struct {
	cli CLI

	showArgs bool
	showLong bool
}

// NewList creates a new List command.
func NewList(cli CLI) *List {
	return &List{
		cli: cli,
	}
}

// Cmds returns the list of commands for the list command.
func (a *List) Cmds() []*cli.Command {
	return []*cli.Command{
		{
			Name:        "ls",
			Usage:       "List targets from an Earthfile",
			UsageText:   "earth [options] ls [<earthfile-ref>]",
			Description: "List targets from an Earthfile.",
			Action:      a.action,
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:        "args",
					Aliases:     []string{"a"},
					Usage:       "Show Arguments",
					Destination: &a.showArgs,
				},
				&cli.BoolFlag{
					Name:        "long",
					Aliases:     []string{"l"},
					Usage:       "Show full target-ref",
					Destination: &a.showLong,
				},
			},
		},
	}
}

func (a *List) action(_ context.Context, cmd *cli.Command) error {
	a.cli.SetCommandName("listTargets")

	if cmd.NArg() > 1 {
		return errors.New("invalid number of arguments provided")
	}

	var targetToParse string
	if cmd.NArg() > 0 {
		targetToParse = cmd.Args().Get(0)
		if !strings.HasPrefix(targetToParse, "/") && !strings.HasPrefix(targetToParse, ".") {
			return errors.New("remote-paths are not currently supported; local paths must start with \"/\" or \".\"")
		}

		if strings.Contains(targetToParse, "+") {
			return errors.New("path cannot contain a +")
		}

		targetToParse = strings.TrimSuffix(targetToParse, "/Earthfile")
	}

	targetToDisplay := targetToParse
	if targetToParse == "" {
		targetToDisplay = "current directory"
	}

	// Parsed rather than resolved: resolving runs git for the remote, hash,
	// branch and tags, and remote references are refused above.
	path, err := findBuildFile(targetToParse)
	if err != nil {
		return fmt.Errorf("unable to locate Earthfile under %s", targetToDisplay)
	}

	src, err := os.ReadFile(path) // #nosec G304 -- the directory the caller named
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	ef, err := earthfile.Parse(path, string(src), earthfile.WithSourceMap())
	if err != nil {
		return err
	}

	targets := make([]string, 0, len(ef.Targets))
	for _, t := range ef.Targets {
		targets = append(targets, t.Name)
	}

	targets = append(targets, earthfile.TargetBase)
	sort.Strings(targets)

	for _, t := range targets {
		var args []string

		if a.showArgs && t != earthfile.TargetBase {
			args, err = earthfile2llb.TargetArgs(ef, t)
			if err != nil {
				return err
			}
		}

		if a.showLong {
			fmt.Printf("%s+%s\n", targetToParse, t)
		} else {
			fmt.Printf("+%s\n", t)
		}

		if a.showArgs {
			for _, arg := range args {
				fmt.Printf("  --%s\n", arg)
			}
		}
	}

	return nil
}

// buildFileNames are the names a project's build file may have, in the order
// buildcontext.detectBuildFile tries them.
var buildFileNames = []string{"Earthfile", "build.earth"}

// findBuildFile locates the file ls should read, given the directory the caller
// named, or "" for none.
//
// Two things the resolver did that <dir>/Earthfile does not, both restored
// here. A caller who names no directory is searched upwards, because ls is run
// from inside a project at least as often as from its root - the same walk
// build does, in buildcontext.resolveLocalRootEarthfile. And a project may
// still call its build file build.earth, which detectBuildFile accepts and
// nothing has deprecated.
func findBuildFile(named string) (string, error) {
	dir := cmp.Or(named, ".")

	// Only a caller who named no directory, matching resolveLocalRootEarthfile:
	// a named one is where the caller says the project is.
	if filepath.Clean(dir) == "." {
		if up, ok := nearestEarthfileDir(); ok {
			dir = up
		}
	}

	for _, name := range buildFileNames {
		p := filepath.Join(dir, name)

		fi, err := os.Stat(p)
		if err == nil && !fi.IsDir() {
			return p, nil
		}
	}

	return "", fs.ErrNotExist
}

// nearestEarthfileDir is the closest directory at or above the working
// directory holding an Earthfile, relative to the working directory.
//
// Earthfile and not build.earth, because that is what the walk it mirrors
// stops on; a directory reached this way is then offered both names.
func nearestEarthfileDir() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}

	for curr := cwd; ; {
		fi, err := os.Stat(filepath.Join(curr, buildFileNames[0]))
		if err == nil && !fi.IsDir() {
			rel, err := filepath.Rel(cwd, curr)
			if err != nil {
				return "", false
			}

			return rel, true
		}

		parent := filepath.Dir(curr)
		if parent == curr {
			return "", false
		}

		curr = parent
	}
}
