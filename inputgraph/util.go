package inputgraph

import (
	"context"
	"errors"
	"strings"

	"github.com/EarthBuild/earthbuild/buildcontext"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// ParseProjectCommand parses a project command from arguments.
func ParseProjectCommand(
	ctx context.Context, target domain.Target, console conslogging.ConsoleLogger,
) (string, string, error) {
	if target.IsRemote() {
		return "", "", errCannotLoadRemoteTarget
	}

	resolver := buildcontext.NewResolver(nil, nil, console, "", "", "", 0, "")

	buildCtx, err := resolver.Resolve(ctx, nil, nil, target)
	if err != nil {
		return "", "", err
	}

	ef := buildCtx.Earthfile

	for _, stmt := range ef.BaseRecipe {
		if stmt.Command != nil && stmt.Command.Name == earthfile.CmdProject {
			args := stmt.Command.Args
			if len(args) != 1 {
				return "", "", errors.New("failed to parse PROJECT command")
			}

			parts := strings.Split(args[0], "/")
			if len(parts) != 2 {
				return "", "", errors.New("failed to parse PROJECT command")
			}

			return parts[0], parts[1], nil
		}
	}

	return "", "", errors.New("PROJECT command is required for remote storage")
}

func copyVisited(m map[string]struct{}) map[string]struct{} {
	m2 := map[string]struct{}{}
	for k := range m {
		m2[k] = struct{}{}
	}

	return m2
}

func uniqStrs(all []string) []string {
	m := map[string]struct{}{}
	for _, v := range all {
		m[v] = struct{}{}
	}

	ret := make([]string, 0, len(m))
	for k := range m {
		ret = append(ret, k)
	}

	return ret
}
