package buildcontext

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/internal/synccache"
	"github.com/EarthBuild/earthbuild/util/gitutil"
	"github.com/EarthBuild/earthbuild/util/llbutil/llbfactory"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

type localResolver struct {
	gitMetaCache      *synccache.Cache[string, *gitutil.GitMetadata] // local path -> *gitutil.GitMetadata
	gitBranchOverride string
	buildFileCache    *synccache.Cache[string, *buildFile] // canonical ref -> *buildFile
	console           conslogging.ConsoleLogger
}

func newLocalResolver(gitBranchOverride string, console conslogging.ConsoleLogger) *localResolver {
	return &localResolver{
		buildFileCache:    synccache.NewCache[string, *buildFile](),
		gitMetaCache:      synccache.NewCache[string, *gitutil.GitMetadata](),
		gitBranchOverride: gitBranchOverride,
		console:           console,
	}
}

func (lr *localResolver) resolveLocal(
	ctx context.Context,
	gwClient gwclient.Client,
	platr *platutil.Resolver,
	ref domain.Reference,
	featureFlagOverrides string,
) (*Data, error) {
	if ref.IsRemote() {
		return nil, fmt.Errorf("unexpected remote target %s", ref.String())
	}

	metadata, err := lr.gitMetaCache.Load(
		ctx, ref.GetLocalPath(),
		func(ctx context.Context) (*gitutil.GitMetadata, error) {
			meta, err := gitutil.Metadata(ctx, ref.GetLocalPath(), lr.gitBranchOverride)
			if err != nil {
				if errors.Is(err, gitutil.ErrNoGitBinary) ||
					errors.Is(err, gitutil.ErrNotAGitDir) ||
					errors.Is(err, gitutil.ErrCouldNotDetectRemote) ||
					errors.Is(err, gitutil.ErrCouldNotDetectGitHash) ||
					errors.Is(err, gitutil.ErrCouldNotDetectGitShortHash) ||
					errors.Is(err, gitutil.ErrCouldNotDetectGitBranch) ||
					errors.Is(err, gitutil.ErrCouldNotDetectGitTags) ||
					errors.Is(err, gitutil.ErrCouldNotDetectGitRefs) {
					// Keep going anyway. Either not a git dir, or git not installed, or
					// remote not detected.
					if errors.Is(err, gitutil.ErrNoGitBinary) {
						lr.console.Warnf("Warning: %s\n", err.Error())
					}
				} else {
					return nil, err
				}
			}

			return meta, nil
		},
	)
	if err != nil {
		return nil, err
	}

	localPath := filepath.FromSlash(ref.GetLocalPath())
	key := localPath

	isDockerfile := strings.HasPrefix(ref.GetName(), DockerfileMetaTarget)
	if isDockerfile {
		// Different key for dockerfiles to include the dockerfile name itself.
		key = ref.String()
	}

	bf, err := lr.buildFileCache.Load(ctx, key, func(context.Context) (*buildFile, error) {
		var buildFilePath string

		buildFilePath, err = detectBuildFile(ref, localPath)
		if err != nil {
			return nil, err
		}

		if isDockerfile {
			return newDockerfileBuild(buildFilePath), nil
		}

		return newEarthfileBuild(buildFilePath, featureFlagOverrides, lr.console)
	})
	if err != nil {
		return nil, err
	}

	data := &Data{
		BuildFilePath: bf.path,
		GitMetadata:   metadata,
		Features:      bf.features,
		Earthfile:     bf.tree,
	}

	if gwClient == nil {
		return data, nil
	}

	// guard against auto-complete code's GetTargetArgs() func which passes in a nil gwClient
	// (but doesn't actually invoke buildkit)
	if _, isTarget := ref.(domain.Target); !isTarget {
		return data, nil
	}

	noImplicitIgnore := bf.features != nil && bf.features.NoImplicitIgnore
	useDockerIgnore := isDockerfile

	ftrs := features.FromContext(ctx)
	if ftrs != nil {
		useDockerIgnore = useDockerIgnore && ftrs.UseDockerIgnore
	}

	excludes, err := readExcludes(ref.GetLocalPath(), noImplicitIgnore, useDockerIgnore)
	if err != nil {
		return nil, err
	}

	data.BuildContextFactory = llbfactory.Local(
		ref.GetLocalPath(),
		llb.ExcludePatterns(excludes),
		llb.Platform(platr.LLBNative()),
		llb.WithCustomNamef("[context %s] local context %s", ref.GetLocalPath(), ref.GetLocalPath()),
	)

	return data, nil
}
