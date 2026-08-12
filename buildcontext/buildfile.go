package buildcontext

import (
	"fmt"
	"strings"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// buildFile holds the path, resolved feature flags, and AST tree of an Earthfile or Dockerfile.
type buildFile struct {
	path     string
	features *features.Features
	tree     earthfile.Tree
}

// newDockerfileBuild creates a buildFile for a Dockerfile target.
func newDockerfileBuild(path string) *buildFile {
	return &buildFile{
		path:     path,
		features: new(features.Features),
	}
}

// newEarthfileBuild reads and parses the Earthfile at path. It parses the file into an AST tree
// with source mapping, extracts VERSION feature flags, outputs any flag warning messages
// to console, applies feature flag overrides, and returns the fully populated buildFile.
func newEarthfileBuild(path, overrides string, console conslogging.ConsoleLogger) (*buildFile, error) {
	tree, err := earthfile.ParseFile(path, earthfile.WithSourceMap())
	if err != nil {
		return nil, err
	}

	ftrs, hasVersion, err := features.Get(tree.Version)
	if err != nil {
		return nil, err
	}

	if !hasVersion {
		return nil, fmt.Errorf("no version specified in %s", path)
	}

	warningStrs, err := ftrs.ProcessFlags()
	if err != nil {
		return nil, err
	}

	if len(warningStrs) > 0 {
		console.Printf(
			"NOTE: The %s feature is enabled by default under VERSION %s, "+
				"and can be safely removed from the VERSION command",
			strings.Join(warningStrs, ", "), ftrs.Version(),
		)
	}

	err = features.ApplyFlagOverrides(ftrs, overrides)
	if err != nil {
		return nil, err
	}

	return &buildFile{
		path:     path,
		features: ftrs,
		tree:     tree,
	}, nil
}
