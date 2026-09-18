package earthfile2llb

import (
	"context"
	"fmt"

	"github.com/EarthBuild/earthbuild/buildcontext"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/util/flagutil"
	"github.com/EarthBuild/earthbuild/util/platutil"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// These are functions that are used for getting information about an Earthfile,
// most notably for `earth doc` and `earth ls` output.

// GetTargets returns a list of targets from an Earthfile.
// Note that the passed in domain.Target's target name is ignored (only the reference to the Earthfile is used).
func GetTargets(
	ctx context.Context, resolver *buildcontext.Resolver, gwClient gwclient.Client, target domain.Target,
) ([]string, error) {
	platr := platutil.NewResolver(platutil.GetUserPlatform())

	bc, err := resolver.Resolve(ctx, gwClient, platr, target)
	if err != nil {
		return nil, fmt.Errorf("resolve build context for target %s: %w", target.String(), err)
	}

	targets := make([]string, 0, len(bc.Earthfile.Targets))
	for _, target := range bc.Earthfile.Targets {
		targets = append(targets, target.Name)
	}

	return targets, nil
}

// GetTargetArgs returns a list of build arguments for a specified target.
func GetTargetArgs(
	ctx context.Context, resolver *buildcontext.Resolver, gwClient gwclient.Client, target domain.Target,
) ([]string, error) {
	platr := platutil.NewResolver(platutil.GetUserPlatform())

	bc, err := resolver.Resolve(ctx, gwClient, platr, target)
	if err != nil {
		return nil, fmt.Errorf("resolve build context for target %s: %w", target.String(), err)
	}

	args, err := TargetArgs(bc.Earthfile, target.Target)
	if err != nil {
		return nil, fmt.Errorf("failed to find %s: %w", target.String(), err)
	}

	return args, nil
}

// TargetArgs returns a list of build argument names defined in the recipe for targetName within ef.
func TargetArgs(ef earthfile.Tree, targetName string) ([]string, error) {
	recipe, isBase, found := targetRecipe(ef, targetName)
	if !found {
		return nil, fmt.Errorf("target %q not found", targetName)
	}

	var args []string

	for _, stmt := range recipe {
		if stmt.Command != nil && stmt.Command.Name == earthfile.CmdArg {
			info, err := ParseArg(*stmt.Command, isBase, false)
			if err != nil {
				return nil, fmt.Errorf("failed to parse ARG arguments %v: %w", stmt.Command.Args, err)
			}

			args = append(args, info.Name)
		}
	}

	return args, nil
}

func targetRecipe(ef earthfile.Tree, name string) (earthfile.Block, bool, bool) {
	if name == earthfile.TargetBase {
		return ef.BaseRecipe, true, true
	}

	for _, tgt := range ef.Targets {
		if tgt.Name == name {
			return tgt.Recipe, false, true
		}
	}

	return nil, false, false
}

// ArgInfo contains metadata describing an ARG command.
type ArgInfo struct {
	DefaultVal  *string
	Name        string
	Description string
	Required    bool
	Global      bool
}

// ParseArg returns the parsed metadata of an ARG command.
func ParseArg(cmd earthfile.Command, isBase, explicitGlobal bool) (ArgInfo, error) {
	if cmd.Name != earthfile.CmdArg {
		return ArgInfo{}, fmt.Errorf("ParseArg was called with non-arg command type '%v'", cmd.Name)
	}

	opts, argName, dflt, err := flagutil.ParseArgArgs(cmd, isBase, explicitGlobal)
	if err != nil {
		return ArgInfo{}, fmt.Errorf("could not parse opts for ARG [%v]: %w", cmd, err)
	}

	return ArgInfo{
		DefaultVal:  dflt,
		Name:        argName,
		Description: opts.Description,
		Required:    opts.Required,
		Global:      opts.Global,
	}, nil
}

// ArtifactName returns the parsed name of a SAVE ARTIFACT command and its local
// name (if any).
func ArtifactName(cmd earthfile.Command) (string, *string, error) {
	if cmd.Name != earthfile.CmdSaveArtifact {
		return "", nil, fmt.Errorf("ArtifactName was called with non-save-artifact command type '%v'", cmd.Name)
	}

	from, to, asLocal, ok := parseSaveArtifactArgs(cmd.Args)
	if !ok {
		return "", nil, fmt.Errorf("could not parse opts for SAVE ARTIFACT [%v]", cmd)
	}

	if to == "./" {
		to = from
	}

	if asLocal == "" {
		return to, nil, nil
	}

	return to, &asLocal, nil
}

// ImageNames returns the parsed names of a SAVE IMAGE command.
func ImageNames(cmd earthfile.Command) ([]string, error) {
	if cmd.Name != earthfile.CmdSaveImage {
		return nil, fmt.Errorf("ImageNames was called with non-save-image command type '%v'", cmd.Name)
	}

	var opts cmdopts.SaveImage

	args, err := flagutil.ParseArgs(string(earthfile.CmdSaveImage), &opts, flagutil.GetArgsCopy(cmd))
	if err != nil {
		return nil, fmt.Errorf("invalid SAVE IMAGE arguments %v: %w", cmd.Args, err)
	}

	return args, nil
}
