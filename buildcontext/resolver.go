package buildcontext

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/cleanup"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/util/fileutil"
	"github.com/EarthBuild/earthbuild/util/gitutil"
	"github.com/EarthBuild/earthbuild/util/llbutil/llbfactory"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/util/syncutil/synccache"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	buildkitgitutil "github.com/moby/buildkit/util/gitutil"
	"github.com/pkg/errors"
)

// DockerfileMetaTarget is a target name prefix which signals the resolver that the build file is a
// dockerfile. The DockerfileMetaTarget is really not a valid earth target otherwise.
const DockerfileMetaTarget = "@dockerfile:"

// Data represents a resolved target's build context data.
type Data struct {
	// BuildContext is the state to use for the build.
	BuildContextFactory llbfactory.Factory
	// Target is the earth reference.
	Ref domain.Reference
	// GitMetadata contains git metadata information.
	GitMetadata *gitutil.GitMetadata
	// LocalDirs is the local dirs map to be passed as part of the buildkit solve.
	LocalDirs map[string]string
	// Features holds the feature state for the build context
	Features *features.Features
	// BuildFilePath is the local path where the Earthfile or Dockerfile can be found.
	BuildFilePath string
	// The parsed Earthfile AST.
	Earthfile earthfile.Tree
}

// Resolver is a build context resolver.
type Resolver struct {
	gr                   *gitResolver
	lr                   *localResolver
	parseCache           *synccache.SyncCache // local path -> AST
	featureFlagOverrides string
	console              conslogging.ConsoleLogger

	inMemoryEarthfiles   map[string]earthfile.Tree
	inMemoryMutex        sync.RWMutex
}

// NewResolver returns a new NewResolver.
func NewResolver(
	cleanCollection *cleanup.Collection,
	gitLookup *GitLookup,
	console conslogging.ConsoleLogger,
	featureFlagOverrides, gitBranchOverride, gitLFSInclude string,
	gitLogLevel buildkitgitutil.GitLogLevel,
	gitImage string,
) *Resolver {
	return &Resolver{
		gr: &gitResolver{
			gitBranchOverride: gitBranchOverride,
			gitImage:          gitImage,
			lfsInclude:        gitLFSInclude,
			logLevel:          gitLogLevel,
			cleanCollection:   cleanCollection,
			projectCache:      synccache.New(),
			buildFileCache:    synccache.New(),
			gitLookup:         gitLookup,
			console:           console,
		},
		lr: &localResolver{
			buildFileCache:    synccache.New(),
			gitMetaCache:      synccache.New(),
			gitBranchOverride: gitBranchOverride,
			console:           console,
		},
		parseCache:           synccache.New(),
		console:              console,
		featureFlagOverrides: featureFlagOverrides,
		inMemoryEarthfiles:   make(map[string]earthfile.Tree),
	}
}

// RegisterInMemoryEarthfile registers an in-memory Earthfile AST for a specific target reference.
func (r *Resolver) RegisterInMemoryEarthfile(targetRef string, tree earthfile.Tree) {
	r.inMemoryMutex.Lock()
	defer r.inMemoryMutex.Unlock()
	if r.inMemoryEarthfiles == nil {
		r.inMemoryEarthfiles = make(map[string]earthfile.Tree)
	}
	r.inMemoryEarthfiles[targetRef] = tree

	// Also register for all other targets defined in this tree
	parts := strings.Split(targetRef, "+")
	if len(parts) > 0 {
		basePath := parts[0]
		for _, target := range tree.Targets {
			r.inMemoryEarthfiles[basePath+"+"+target.Name] = tree
		}
	}
}

// ExpandWildcard will expand a wildcard BUILD target in a local path or remote
// Git repository. Local and remote targets are treated differently. For local
// targets, we need to join the two targets in order to derive the full relative
// path. This is then used when globbing for matches. The paths are then made
// relative to the parent target for resolution by the caller.
func (r *Resolver) ExpandWildcard(
	ctx context.Context, gwClient gwclient.Client, platr *platutil.Resolver, parentTarget, target domain.Target,
) ([]string, error) {
	if parentTarget.IsRemote() {
		matches, err := r.gr.expandWildcard(ctx, gwClient, platr, parentTarget, target.GetLocalPath())
		if err != nil {
			return nil, errors.Wrap(err, "failed to expand remote BUILD target path")
		}

		return matches, nil
	}

	// For local targets, we need to determine the full path relative to the
	// working directory of earth in order to glob for matching paths. We can
	// get this path by joining the targets. The child target will likely still
	// include *'s (expanded below), but that shouldn't be a problem.
	ref, err := domain.JoinReferences(parentTarget, target)
	if err != nil {
		return nil, errors.Wrap(err, "failed to join references")
	}

	target, ok := ref.(domain.Target)
	if !ok {
		return nil, fmt.Errorf("want domain.Target, got %T", ref)
	}

	matches, err := fileutil.GlobDirs(target.GetLocalPath())
	if err != nil {
		return nil, errors.Wrap(err, "failed to expand BUILD target path")
	}

	// Here, the relative path is reconstructed from the glob results and the
	// parent target's path. This is done because the Earthfile resolution
	// requires a relative target path.
	ret := make([]string, 0, len(matches))
	for _, match := range matches {
		rel, err := filepath.Rel(parentTarget.GetLocalPath(), match)
		if err != nil {
			return nil, errors.Wrap(err, "failed to resolve relative path")
		}

		ret = append(ret, rel)
	}

	return ret, nil
}

// Resolve returns resolved context data for a given earth reference. If the reference is a target,
// then the context will include a build context and possibly additional local directories.
func (r *Resolver) Resolve(
	ctx context.Context, gwClient gwclient.Client, platr *platutil.Resolver, ref domain.Reference,
) (*Data, error) {
	r.inMemoryMutex.RLock()
	inMemTree, isInMemory := r.inMemoryEarthfiles[ref.String()]
	r.inMemoryMutex.RUnlock()

	if isInMemory {
		localDirs := make(map[string]string)
		if _, isTarget := ref.(domain.Target); isTarget {
			localDirs[ref.GetLocalPath()] = ref.GetLocalPath()
		}

		ftrs, _, err := features.Get(&earthfile.Version{Args: []string{"0.7"}})
		if err != nil {
			return nil, errors.Wrap(err, "failed to get features for in-memory AST")
		}

		excludes, err := readExcludes(ref.GetLocalPath(), ftrs.NoImplicitIgnore, false)
		if err != nil {
			return nil, err
		}

		buildFilePath := ""
		if inMemTree.SourceLocation != nil {
			buildFilePath = inMemTree.SourceLocation.File
		} else {
			buildFilePath = filepath.Join(ref.GetLocalPath(), "Dockerfile")
		}

		data := &Data{
			BuildFilePath: buildFilePath,
			Earthfile:     inMemTree,
			Features:      ftrs,
			LocalDirs:     localDirs,
			Ref:           ref,
		}

		if gwClient != nil {
			data.BuildContextFactory = llbfactory.Local(
				ref.GetLocalPath(),
				llb.ExcludePatterns(excludes),
				llb.Platform(platr.LLBNative()),
				llb.WithCustomNamef("[context %s] local context %s", ref.GetLocalPath(), ref.GetLocalPath()),
			)
		}

		return data, nil
	}

	if ref.IsUnresolvedImportReference() {
		return nil, errors.Errorf("cannot resolve non-dereferenced import ref %s", ref.String())
	}

	var (
		d   *Data
		err error
	)

	localDirs := make(map[string]string)

	if ref.IsRemote() {
		// Remote.
		d, err = r.gr.resolveEarthProject(ctx, gwClient, platr, ref, r.featureFlagOverrides)
		if err != nil {
			return nil, err
		}
	} else {
		// Local.
		if _, isTarget := ref.(domain.Target); isTarget {
			localDirs[ref.GetLocalPath()] = ref.GetLocalPath()
		}

		d, err = r.lr.resolveLocal(ctx, gwClient, platr, ref, r.featureFlagOverrides)
		if err != nil {
			return nil, err
		}
	}

	d.Ref = gitutil.ReferenceWithGitMeta(ref, d.GitMetadata)

	d.LocalDirs = localDirs
	if !strings.HasPrefix(ref.GetName(), DockerfileMetaTarget) {
		d.Earthfile, err = r.parseEarthfile(ctx, d.BuildFilePath)
		if err != nil {
			return nil, err
		}
	}

	return d, nil
}

func (r *Resolver) parseEarthfile(ctx context.Context, path string) (earthfile.Tree, error) {
	path = filepath.Clean(path)

	efValue, err := r.parseCache.Do(ctx, path, func(_ context.Context, k any) (any, error) {
		filePath, ok := k.(string)
		if !ok {
			return nil, fmt.Errorf("want string, got %T", k)
		}

		return earthfile.ParseFile(filePath, earthfile.WithSourceMap())
	})
	if err != nil {
		return earthfile.Tree{}, err
	}

	ef, ok := efValue.(earthfile.Tree)
	if !ok {
		return earthfile.Tree{}, errors.Errorf("want earthfile.Tree, got %T", efValue)
	}

	return ef, nil
}
