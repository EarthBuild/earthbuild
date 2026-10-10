// Package builder orchestrates the top-level resolution and execution of earth targets and commands.
package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/EarthBuild/earthbuild/buildcontext"
	"github.com/EarthBuild/earthbuild/buildcontext/provider"
	"github.com/EarthBuild/earthbuild/cleanup"
	"github.com/EarthBuild/earthbuild/cmd/earth/bk"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logbus/solvermon"
	"github.com/EarthBuild/earthbuild/regproxy"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/dockerutil"
	"github.com/EarthBuild/earthbuild/util/gatewaycrafter"
	"github.com/EarthBuild/earthbuild/util/gwclientlogger"
	"github.com/EarthBuild/earthbuild/util/llbutil"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/EarthBuild/earthbuild/util/llbutil/secretprovider"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/util/saveartifactlocally"
	"github.com/EarthBuild/earthbuild/util/syncutil/semutil"
	"github.com/EarthBuild/earthbuild/variables"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/apicaps"
	"github.com/moby/buildkit/util/entitlements"
	buildkitgitutil "github.com/moby/buildkit/util/gitutil"
	"golang.org/x/sync/errgroup"
)

const (
	// PhaseInit is the phase text for the init phase.
	PhaseInit = "Init 🚀"
	// PhaseBuild is the phase text for the build phase.
	PhaseBuild = "Build 🔧"
	// PhasePush is the phase text for the push phase.
	PhasePush = "Push Summary ⏫"
	// PhaseOutput is the phase text for the output phase.
	PhaseOutput = "Local Output Summary 🎁"
)

// Opt represent builder options.
type Opt struct {
	BuildkitSkipper                       bk.BuildkitSkipper
	Engine                                *engine.Client
	Parallelism                           semutil.Semaphore
	OverridingVars                        *variables.Scope
	GitLookup                             *buildcontext.GitLookup
	BuildContextProvider                  *provider.BuildContextProvider
	InternalSecretStore                   *secretprovider.MutableMapStore
	CacheImports                          *states.CacheImports
	BkClient                              *client.Client
	Log                                   *conslogging.ConsoleLogger
	LogBusSolverMonitor                   *solvermon.SolverMonitor
	CleanCollection                       *cleanup.Collection
	GitImage                              string
	DarwinProxyImage                      string
	MaxCacheExport                        string
	GitLFSInclude                         string
	LocalRegistryAddr                     string
	GitBranchOverride                     string
	FeatureFlagOverrides                  string
	CacheExport                           string
	Enttlmnts                             []entitlements.Entitlement
	Attachables                           []session.Attachable
	DarwinProxyWait                       time.Duration
	GitLogLevel                           buildkitgitutil.GitLogLevel
	ImageResolveMode                      llb.ResolveMode
	Verbose                               bool
	DisableRemoteRegistryProxy            bool
	NoCache                               bool
	ParallelConversion                    bool
	UseFakeDep                            bool
	InteractiveDebugging                  bool
	InteractiveDebuggingDebugLevelLogging bool
	DisableNoOutputUpdates                bool
	Strict                                bool
	UseInlineCache                        bool
	SaveInlineCache                       bool
	NoAutoSkip                            bool
}

// ProjectAdder provides an interface for adding projects.
type ProjectAdder interface {
	AddProject(org, project string)
}

// BuildOpt is a collection of build options.
type BuildOpt struct {
	ProjectAdder               ProjectAdder
	OnlyArtifact               *domain.Artifact
	Logbus                     *logbus.Bus
	LocalArtifactWhiteList     *gatewaycrafter.LocalArtifactWhiteList
	PlatformResolver           *platutil.Resolver
	BuiltinArgs                variables.DefaultArgs
	OnlyArtifactDestPath       string
	Runner                     string
	Export                     earthfile2llb.Export
	OnlyFinalTargetImages      bool
	EnableGatewayClientLogging bool
	GlobalWaitBlockFtr         bool
	Push                       bool
	PrintPhases                bool
	AllowPrivileged            bool
}

// imagePlanOpt returns the build-wide options that earthfile2llb.PlanImage acts
// on. The same value is handed to the converter, so both ask the same question.
func (opt BuildOpt) imagePlanOpt() earthfile2llb.ImagePlanOpt {
	return earthfile2llb.ImagePlanOpt{
		Export:                opt.Export,
		Push:                  opt.Push,
		OnlyArtifact:          opt.OnlyArtifact != nil,
		OnlyFinalTargetImages: opt.OnlyFinalTargetImages,
	}
}

// planImage decides the fate of one SAVE IMAGE. See earthfile2llb.PlanImage,
// which is the only place that decision is made.
func planImage(
	opt BuildOpt, sts *states.SingleTarget, isFinal bool, saveImage states.SaveImage,
) earthfile2llb.ImagePlan {
	return earthfile2llb.PlanImage(opt.imagePlanOpt(), sts, isFinal, saveImage)
}

// isMainHandledByImage reports whether mts.Final.MainState will already be solved
// and exported as part of an image plan, making a separate "main" reference redundant.
func isMainHandledByImage(
	mts *states.MultiTarget,
	opt BuildOpt,
	cacheExport string,
	targetImages func(*states.SingleTarget) []states.SaveImage,
) bool {
	if mts == nil || mts.Final == nil || mts.Final.MainState.Output() == nil {
		return true
	}

	for _, sts := range mts.All() {
		for _, saveImage := range targetImages(sts) {
			plan := planImage(opt, sts, sts == mts.Final, saveImage)
			if !plan.SolvedByBuilder(saveImage, cacheExport != "") {
				continue
			}

			if saveImage.State.Output() == mts.Final.MainState.Output() {
				return true
			}
		}
	}

	return false
}

// Builder executes earth builds.
type Builder struct {
	outDir     string
	s          *solver
	resolver   *buildcontext.Resolver
	opt        Opt
	outDirOnce sync.Once
	builtMain  bool
}

// NewBuilder returns a new earth Builder.
func NewBuilder(opt Opt) (*Builder, error) {
	b := &Builder{
		s: &solver{
			logbusSM:        opt.LogBusSolverMonitor,
			bkClient:        opt.BkClient,
			cacheImports:    opt.CacheImports,
			cacheExport:     opt.CacheExport,
			maxCacheExport:  opt.MaxCacheExport,
			attachables:     opt.Attachables,
			enttlmnts:       opt.Enttlmnts,
			saveInlineCache: opt.SaveInlineCache,
		},
		opt:      opt,
		resolver: nil, // initialized below
	}
	b.resolver = buildcontext.NewResolver(
		opt.CleanCollection, opt.GitLookup, opt.Log, opt.FeatureFlagOverrides, opt.GitBranchOverride,
		opt.GitLFSInclude, opt.GitLogLevel, opt.GitImage,
	)

	return b, nil
}

// BuildTarget executes the build of a given earth target.
func (b *Builder) BuildTarget(ctx context.Context, target domain.Target, opt BuildOpt) (*states.MultiTarget, error) {
	mts, err := b.convertAndBuild(ctx, target, opt)
	if err != nil {
		return nil, err
	}

	return mts, nil
}

func (b *Builder) startRegistryProxy(ctx context.Context, caps apicaps.CapSet) (func(), bool) {
	cons := b.opt.Log.WithPrefix("registry-proxy")

	if b.opt.DisableRemoteRegistryProxy {
		cons.VerbosePrintf("Registry proxy disabled via --disable-remote-registry-proxy")
		return nil, false
	}

	err := caps.Supports(pb.CapEarthlyRegistryProxy)
	if err != nil {
		cons.Print(err.Error())
		return nil, false
	}

	meta := b.opt.Engine.Metadata()

	if !meta.Scheme.SupportsRegistryProxy() {
		cons.Printf("Registry proxy not supported on %s. Falling back to tar-based outputs.", meta.Name)
		return nil, false
	}

	useProxy, err := useSecondaryProxy()
	if err != nil {
		cons.Printf("Failed to check for registry proxy support: %v", err)
		return nil, false
	}

	controller := regproxy.NewController(
		b.s.bkClient.RegistryClient(),
		b.opt.Engine,
		useProxy,
		b.opt.DarwinProxyImage,
		b.opt.DarwinProxyWait,
		cons,
	)

	addr, closeFn, err := controller.Start(ctx)
	if err != nil {
		cons.Printf("Failed to start registry proxy: %v", err)
		return nil, false
	}

	b.opt.LocalRegistryAddr = addr

	return closeFn, true
}

// useSecondaryProxy detects if we're on Mac (Darwin) or running on Windows in WSL2 or otherwise.
func useSecondaryProxy() (bool, error) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return true, nil
	}

	versionFile := "/proc/version"

	_, err := os.Stat(versionFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}

		return false, fmt.Errorf("failed to stat %s: %w", versionFile, err)
	}

	f, err := os.Open(versionFile)
	if err != nil {
		return false, fmt.Errorf("failed to open %s: %w", versionFile, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return false, fmt.Errorf("failed to read %s: %w", versionFile, err)
	}

	s := string(data)

	return strings.Contains(s, "WSL2"), nil
}

func (b *Builder) convertAndBuild(
	ctx context.Context, target domain.Target, opt BuildOpt,
) (_ *states.MultiTarget, retErr error) {
	var (
		sharedLocalStateCache = earthfile2llb.NewSharedLocalStateCache()
		featureFlagOverrides  = b.opt.FeatureFlagOverrides
		manifestLists         = make(map[string][]dockerutil.Manifest) // parent image -> child images
		platformImgNames      = make(map[string]struct{})              // ensure that these are unique
		singPlatImgNames      = make(map[string]struct{})              // ensure that these are unique
		exportCoordinator     = gatewaycrafter.NewExportCoordinator()

		// builderExports are the image exports builder.go took on, settled once
		// buildMainMulti is done.
		builderExports []*states.ImageExport

		// pendingExports keeps every image export a target is waiting on to end.
		pendingExports = &states.PendingExports{}

		// dirIDs maps a dirIndex to a dirID; the "dir-id" field was introduced
		// to accommodate parallelism in the WAIT/END PopWaitBlock handling
		dirIDs = map[int]string{}
	)

	// If the build fails before an image export has run (a conversion error,
	// or a failure in buildMainMulti), the targets waiting on that export would
	// never end. End them as cancelled instead.
	defer func() {
		if retErr != nil {
			pendingExports.Abort(ctx, retErr)
		}
	}()

	var (
		depIndex   = 0
		imageIndex = 0
		dirIndex   = 0
	)

	// Delay closing the registry proxy server until after the build function
	// returns. This can be deferred within the build function once global wait
	// block support is enabled.
	stopRegistryProxyFunc := func() {}

	defer func() {
		stopRegistryProxyFunc()
	}()

	var mts *states.MultiTarget

	buildFunc := func(childCtx context.Context, gwClient gwclient.Client) (*gwclient.Result, error) {
		if opt.EnableGatewayClientLogging {
			gwClient = gwclientlogger.New(gwClient)
		}

		var err error

		caps := gwClient.BuildOpts().LLBCaps

		if stopProxy, ok := b.startRegistryProxy(ctx, caps); ok {
			stopRegistryProxyFunc = stopProxy
		}

		if !b.builtMain {
			opt := earthfile2llb.ConvertOpt{
				GwClient:                             gwClient,
				Resolver:                             b.resolver,
				ImageResolveMode:                     b.opt.ImageResolveMode,
				CleanCollection:                      b.opt.CleanCollection,
				PlatformResolver:                     opt.PlatformResolver.SubResolver(opt.PlatformResolver.Current()),
				DockerImageSolverTar:                 newTarImageSolver(b.opt, b.s.logbusSM),
				MultiImageSolver:                     newMultiImageSolver(b.opt, b.s.logbusSM),
				OverridingVars:                       b.opt.OverridingVars,
				BuildContextProvider:                 b.opt.BuildContextProvider,
				CacheImports:                         b.opt.CacheImports,
				UseInlineCache:                       b.opt.UseInlineCache,
				UseFakeDep:                           b.opt.UseFakeDep,
				AllowLocally:                         !b.opt.Strict,
				AllowInteractive:                     !b.opt.Strict,
				AllowPrivileged:                      opt.AllowPrivileged,
				ParallelConversion:                   b.opt.ParallelConversion,
				Parallelism:                          b.opt.Parallelism,
				Log:                                  b.opt.Log,
				GitLookup:                            b.opt.GitLookup,
				FeatureFlagOverrides:                 featureFlagOverrides,
				LocalStateCache:                      sharedLocalStateCache,
				BuiltinArgs:                          opt.BuiltinArgs,
				NoCache:                              b.opt.NoCache,
				Engine:                               b.opt.Engine,
				UseLocalRegistry:                     (b.opt.LocalRegistryAddr != ""),
				LocalRegistryAddr:                    b.opt.LocalRegistryAddr,
				Export:                               opt.Export,
				SaveReferenced:                       true,
				OnlyFinalTargetImages:                opt.OnlyFinalTargetImages,
				DoPushes:                             opt.Push,
				ImagePlan:                            opt.imagePlanOpt(),
				ExportCoordinator:                    exportCoordinator,
				PendingExports:                       pendingExports,
				LocalArtifactWhiteList:               opt.LocalArtifactWhiteList,
				InternalSecretStore:                  b.opt.InternalSecretStore,
				TempEarthOutDir:                      b.tempEarthOutDir,
				GlobalWaitBlockFtr:                   opt.GlobalWaitBlockFtr,
				LLBCaps:                              &caps,
				InteractiveDebuggerEnabled:           b.opt.InteractiveDebugging,
				InteractiveDebuggerDebugLevelLogging: b.opt.InteractiveDebuggingDebugLevelLogging,
				Logbus:                               opt.Logbus,
				Runner:                               opt.Runner,
				ProjectAdder:                         opt.ProjectAdder,
				FilesWithCommandRenameWarning:        make(map[string]struct{}),
				BuildkitSkipper:                      b.opt.BuildkitSkipper,
				NoAutoSkip:                           b.opt.NoAutoSkip,
			}

			mts, err = earthfile2llb.Earthfile2LLB(childCtx, target, opt, true)
			if err != nil {
				return nil, err
			}
		}

		if opt.GlobalWaitBlockFtr {
			if opt.OnlyArtifact != nil || opt.OnlyFinalTargetImages {
				b.opt.Log.Printf("builder.go bf code is still required for OnlyArtifact or " +
					"OnlyFinalTargetImages modes (GlobalWaitBlockFtr has no effect)\n")
			} else {
				b.opt.Log.Printf("skipping builder.go bf code due to GlobalWaitBlockFtr\n")
				return nil, nil
			}
		}

		// WARNING: the code below is deprecated, and will eventually be removed, in favour of wait_block.go
		// This code is only used when dealing with VERSION 0.5 and 0.6; once these reach end-of-life, we can
		// delete the code below.

		// NOTE: this code is still required to support remote caching; it can't be removed until
		// https://github.com/earthly/earthly/issues/2178 is fixed.

		// *** DO NOT ADD CODE TO THE bf BELOW ***

		gwCrafter := gatewaycrafter.NewGatewayCrafter()

		if !b.builtMain && !isMainHandledByImage(mts, opt, b.opt.CacheExport, b.targetPhaseImages) {
			ref, err := b.stateToRef(childCtx, gwClient, mts.Final.MainState, mts.Final.PlatformResolver)
			if err != nil {
				return nil, err
			}

			gwCrafter.AddRef("main", ref)
		}

		if opt.Export.Artifacts() && opt.OnlyArtifact != nil && !opt.OnlyFinalTargetImages {
			ref, err := b.stateToRef(childCtx, gwClient, mts.Final.ArtifactsState, mts.Final.PlatformResolver)
			if err != nil {
				return nil, err
			}

			refKey := "final-artifact"
			refPrefix := "ref/" + refKey
			gwCrafter.AddRef(refKey, ref)
			gwCrafter.AddMeta(refPrefix+"/export-dir", []byte("true"))
			gwCrafter.AddMeta(refPrefix+"/final-artifact", []byte("true"))
		}

		images, takeErr := takeBuilderImages(opt, mts, b.opt.CacheExport != "", b.targetPhaseImages)
		if takeErr != nil {
			return nil, takeErr
		}

		for _, img := range images {
			sts, saveImage := img.sts, img.saveImage

			if saveImage.Export != nil && !img.member {
				builderExports = append(builderExports, saveImage.Export)
			}

			shouldExport, shouldPush := img.plan.Export, img.plan.Push

			if img.member && !shouldPush {
				// Only in the local image's manifest list: a wait block loaded it.
				manifest, err := builderImageManifest(img)
				if err != nil {
					return nil, err
				}

				manifestLists[saveImage.DockerTag] = append(manifestLists[saveImage.DockerTag], manifest)

				continue
			}

			// A member was solved by the wait block that pushed it; reuse that ref.
			ref, err := saveImage.Export.Ref(childCtx, func(solveCtx context.Context) (gwclient.Reference, error) {
				return b.stateToRef(solveCtx, gwClient, saveImage.State, sts.PlatformResolver)
			})
			if err != nil {
				return nil, err
			}

			//nolint:nestif // TODO(jhorsts): simplify
			if img.multiPlatform {
				resolvedPlat := builderImagePlatform(img)
				platformStr := resolvedPlat.String()

				platformImgName, err := llbutil.PlatformSpecificImageName(saveImage.DockerTag, resolvedPlat)
				if err != nil {
					return nil, err
				}

				if saveImage.CheckDuplicate && saveImage.DockerTag != "" && !img.member {
					if _, found := platformImgNames[platformImgName]; found {
						return nil, fmt.Errorf(
							"image %s is defined multiple times for the same platform (%s)",
							saveImage.DockerTag, platformImgName,
						)
					}

					platformImgNames[platformImgName] = struct{}{}
				}
				// Image has platform set - need to use manifest lists.
				// Need to push as a single multi-manifest image, but output locally as
				// separate images.
				// (docker load does not support tars with manifest lists).

				// For push.
				if shouldPush {
					_, err = gwCrafter.AddPushImageEntry(
						ref, imageIndex, saveImage.DockerTag, shouldPush, saveImage.InsecurePush,
						saveImage.Image, []byte(platformStr),
					)
					if err != nil {
						return nil, err
					}

					imageIndex++
				}

				// For local. A member was loaded by the wait block that exported it.
				if shouldExport && img.member {
					manifestLists[saveImage.DockerTag] = append(
						manifestLists[saveImage.DockerTag], dockerutil.Manifest{
							ImageName: platformImgName,
							Platform:  resolvedPlat,
						},
					)
				} else if shouldExport {
					refPrefix, err := gwCrafter.
						AddPushImageEntry(ref, imageIndex, platformImgName, false, false, saveImage.Image, nil)
					if err != nil {
						return nil, err
					}

					imageIndex++

					localRegPullID := exportCoordinator.AddImage(gwClient.BuildOpts().SessionID, platformImgName, nil)
					if b.opt.LocalRegistryAddr != "" {
						gwCrafter.AddMeta(refPrefix+"/export-image-local-registry", []byte(localRegPullID))
					} else {
						gwCrafter.AddMeta(refPrefix+"/export-image", []byte("true"))
					}

					manifestLists[saveImage.DockerTag] = append(
						manifestLists[saveImage.DockerTag], dockerutil.Manifest{
							ImageName: platformImgName,
							Platform:  resolvedPlat,
						},
					)
				}
			} else {
				if saveImage.CheckDuplicate && saveImage.DockerTag != "" {
					if _, found := singPlatImgNames[saveImage.DockerTag]; found {
						return nil, fmt.Errorf(
							"image %s is defined multiple times for the same default platform",
							saveImage.DockerTag,
						)
					}

					singPlatImgNames[saveImage.DockerTag] = struct{}{}
				}

				localRegPullID := exportCoordinator.AddImage(gwClient.BuildOpts().SessionID, saveImage.DockerTag, nil)

				refPrefix, err := gwCrafter.AddPushImageEntry(
					ref, imageIndex, saveImage.DockerTag, shouldPush, saveImage.InsecurePush, saveImage.Image, nil,
				)
				if err != nil {
					return nil, err
				}

				imageIndex++

				if shouldExport {
					if b.opt.LocalRegistryAddr != "" {
						gwCrafter.AddMeta(refPrefix+"/export-image-local-registry", []byte(localRegPullID))
					} else {
						gwCrafter.AddMeta(refPrefix+"/export-image", []byte("true"))
					}
				}
			}
		}

		for _, sts := range mts.All() {
			hasRunPush := (sts.GetDoPushes() && sts.RunPush.HasState)
			if (sts.HasDangling && !b.opt.UseFakeDep) || (b.builtMain && hasRunPush) {
				depRef, err := b.stateToRef(childCtx, gwClient, b.targetPhaseState(sts), sts.PlatformResolver)
				if err != nil {
					return nil, err
				}

				refKey := fmt.Sprintf("dep-%d", depIndex)
				gwCrafter.AddRef(refKey, depRef)

				depIndex++
			}

			performSaveLocals := (opt.Export.Artifacts() &&
				!opt.OnlyFinalTargetImages &&
				opt.OnlyArtifact == nil &&
				sts.GetDoSaves())
			if performSaveLocals {
				for _, saveLocal := range b.targetPhaseArtifacts(sts) {
					ref, err := b.artifactStateToRef(
						childCtx, gwClient, sts.SeparateArtifactsState[saveLocal.Index],
						sts.PlatformResolver,
					)
					if err != nil {
						return nil, err
					}

					artifact := domain.Artifact{
						Target:   sts.Target,
						Artifact: saveLocal.ArtifactPath,
					}

					dirID, err := gwCrafter.
						AddSaveArtifactLocal(ref, dirIndex, artifact.String(), saveLocal.ArtifactPath, saveLocal.DestPath)
					if err != nil {
						return nil, err
					}

					dirIDs[dirIndex] = dirID

					opt.LocalArtifactWhiteList.Add(saveLocal.DestPath)

					dirIndex++
				}
			}

			targetInteractiveSession := b.targetPhaseInteractiveSession(sts)
			if targetInteractiveSession.Initialized && targetInteractiveSession.Kind == states.SessionEphemeral {
				ref, err := b.stateToRef(ctx, gwClient, targetInteractiveSession.State, sts.PlatformResolver)
				gwCrafter.AddRef("ephemeral", ref)

				if err != nil {
					return nil, err
				}
			}
		}

		return gwCrafter.GetResult(), nil
	}
	// loadedImageKeys are the export coordinator keys of the per-platform images
	// loaded into the local container engine so far, from a tar or pulled from
	// the local registry.
	loadedImageKeys := map[string]struct{}{}

	var exportedImagesMutex sync.Mutex

	// isLoaded must be called with exportedImagesMutex held.
	isLoaded := func(key string) bool {
		_, ok := loadedImageKeys[key]
		return ok
	}

	onImageDone := func(manifestKey, waitFor string) error {
		exportedImagesMutex.Lock()
		defer exportedImagesMutex.Unlock()

		loadedImageKeys[manifestKey] = struct{}{}
		waitForKeys := strings.Split(waitFor, " ")

		for _, manifestKey := range waitForKeys {
			if !isLoaded(manifestKey) {
				return nil
			}
		}

		manifests, err := exportCoordinator.ManifestLists(waitForKeys, isLoaded)
		if err != nil {
			return fmt.Errorf("onImageDone: %w", err)
		}

		for parentImageName, children := range manifests {
			if opt.PlatformResolver == nil {
				panic("platform resolver is nil")
			}

			err = dockerutil.LoadDockerManifest(
				ctx, b.opt.Log, b.opt.Engine, parentImageName, children, opt.PlatformResolver,
			)
			if err != nil {
				return err
			}
		}

		return nil
	}
	onImage := func(
		childCtx context.Context, eg *errgroup.Group, _, waitFor, manifestKey string,
	) (io.WriteCloser, error) {
		pipeR, pipeW := io.Pipe()

		eg.Go(func() error {
			defer pipeR.Close()

			err := dockerutil.LoadDockerTar(childCtx, b.opt.Engine, pipeR)
			if err != nil {
				return fmt.Errorf("load docker tar: %w", err)
			}

			if manifestKey == "" {
				return nil
			}

			return onImageDone(manifestKey, waitFor)
		})

		return pipeW, nil
	}
	onArtifact := func(_ context.Context, index string, _ domain.Artifact, _, destPath string) (string, error) {
		if !opt.LocalArtifactWhiteList.Exists(destPath) {
			err := fmt.Errorf("dest path %s is not in the whitelist: %+v", destPath, opt.LocalArtifactWhiteList.AsList())
			return "", err
		}

		outDir, err := b.tempEarthOutDir()
		if err != nil {
			return "", err
		}

		artifactDir := filepath.Join(outDir, "index-"+index)

		err = os.MkdirAll(artifactDir, 0o755) // #nosec G301
		if err != nil {
			return "", fmt.Errorf("create dir %s: %w", artifactDir, err)
		}

		return artifactDir, nil
	}
	onFinalArtifact := func(context.Context) (string, error) {
		return b.tempEarthOutDir()
	}
	onPull := func(childCtx context.Context, imagesToPull []string, _ map[string]string) error {
		if b.opt.LocalRegistryAddr == "" {
			return nil
		}

		pullMap := make(map[string]string)

		for _, imgToPull := range imagesToPull {
			manifest, dockerTag, ok := exportCoordinator.GetImage(imgToPull)
			if !ok {
				return fmt.Errorf("unrecognized image to pull %s", imgToPull)
			}

			if manifest != nil {
				pullMap[imgToPull] = manifest.ImageName
			} else {
				pullMap[imgToPull] = dockerTag
			}
		}

		err := dockerutil.DockerPullLocalImages(childCtx, b.opt.Engine, b.opt.LocalRegistryAddr, pullMap)
		if err != nil {
			return err
		}

		exportedImagesMutex.Lock()

		for _, imgToPull := range imagesToPull {
			loadedImageKeys[imgToPull] = struct{}{}
		}

		manifests, err := exportCoordinator.ManifestLists(imagesToPull, isLoaded)
		exportedImagesMutex.Unlock()

		if err != nil {
			return err
		}

		for parentImageName, children := range manifests {
			if opt.PlatformResolver == nil {
				panic("platform resolver is nil")
			}

			err = dockerutil.LoadDockerManifest(
				ctx, b.opt.Log, b.opt.Engine, parentImageName, children, opt.PlatformResolver,
			)
			if err != nil {
				return err
			}
		}

		return nil
	}

	if opt.PrintPhases {
		b.opt.Log.PrintPhaseHeader(PhaseBuild, false, "")
	}

	err := b.s.buildMainMulti(ctx, buildFunc, onImage, onArtifact, onFinalArtifact, onPull, b.opt.Log)

	// A target whose main state only this export solves has not executed until
	// now; see earthfile2llb's Converter.FinalizeStates.
	for _, export := range builderExports {
		export.Outcome.Settle(ctx, err)
	}

	if err != nil {
		return nil, fmt.Errorf("build main: %w", err)
	}

	if opt.PrintPhases {
		b.opt.Log.PrintPhaseFooter(PhaseBuild)
	}

	b.builtMain = true

	if opt.PrintPhases {
		b.opt.Log.PrintPhaseHeader(PhasePush, !opt.Push, "")

		if !opt.Push {
			b.opt.Log.Printf("To enable pushing use earthly --push\n")
		}
	}

	if opt.Push && opt.OnlyArtifact == nil && !opt.OnlyFinalTargetImages {
		hasRunPush := false

		for _, sts := range mts.All() {
			if sts.GetDoPushes() && sts.RunPush.HasState {
				hasRunPush = true
				break
			}
		}

		if hasRunPush {
			err = b.s.buildMainMulti(ctx, buildFunc, onImage, onArtifact, onFinalArtifact, onPull, b.opt.Log)
			if err != nil {
				return nil, fmt.Errorf("build push: %w", err)
			}
		}
	}

	pushConsole := conslogging.NewBufferedLogger(b.opt.Log)
	outputConsole := conslogging.NewBufferedLogger(b.opt.Log)
	outputPhaseSpecial := ""

	switch {
	case !opt.Export.Artifacts():
		// noop
	case opt.OnlyArtifact != nil:
		if mts.Final.GetDoSaves() {
			outputPhaseSpecial = "single artifact"

			var outDir string

			outDir, err = b.tempEarthOutDir()
			if err != nil {
				return nil, err
			}

			err = saveartifactlocally.SaveArtifactLocally(
				ctx, exportCoordinator, b.opt.Log, *opt.OnlyArtifact, outDir, opt.OnlyArtifactDestPath, mts.Final.ID, false,
			)
			if err != nil {
				return nil, err
			}
		}
	case opt.OnlyFinalTargetImages:
		outputPhaseSpecial = "single image"

		for _, saveImage := range mts.Final.SaveImages {
			plan := planImage(opt, mts.Final, true, saveImage)
			shouldExport, shouldPush := plan.Export, plan.Push

			if saveImage.BuilderSkips() || !shouldPush && !shouldExport {
				continue
			}

			if shouldPush {
				exportCoordinator.
					AddPushedImageSummary(mts.Final.Target.StringCanonical(), saveImage.DockerTag, b.opt.Log.Salt(), true)
			}

			if saveImage.Push && !opt.Push {
				exportCoordinator.
					AddPushedImageSummary(mts.Final.Target.StringCanonical(), saveImage.DockerTag, b.opt.Log.Salt(), false)
			}

			if shouldExport {
				exportCoordinator.
					AddLocalOutputSummary(mts.Final.Target.StringCanonical(), saveImage.DockerTag, b.opt.Log.Salt())
			}
		}
	default:
		// This needs to match with the same index used during output.
		// TODO: This is a little brittle to future code changes.
		dirIndex := 0

		for _, sts := range mts.All() {
			for _, saveImage := range sts.SaveImages {
				plan := planImage(opt, sts, sts == mts.Final, saveImage)
				shouldExport, shouldPush := plan.Export, plan.Push

				if saveImage.BuilderSkips() || !shouldPush && !shouldExport {
					continue
				}

				if shouldPush {
					exportCoordinator.AddPushedImageSummary(sts.Target.StringCanonical(), saveImage.DockerTag, sts.ID, true)
				}

				if saveImage.Push && !opt.Push && !sts.Target.IsRemote() {
					exportCoordinator.AddPushedImageSummary(sts.Target.StringCanonical(), saveImage.DockerTag, sts.ID, false)
				}

				if shouldExport {
					exportCoordinator.AddLocalOutputSummary(sts.Target.StringCanonical(), saveImage.DockerTag, sts.ID)
				}
			}

			if sts.GetDoSaves() {
				for _, saveLocal := range sts.SaveLocals {
					var outDir string

					outDir, err = b.tempEarthOutDir()
					if err != nil {
						return nil, err
					}

					dirID, ok := dirIDs[dirIndex]
					if !ok {
						return nil, fmt.Errorf("failed to map dir index %d", dirIndex)
					}

					artifactDir := filepath.Join(outDir, "index-"+dirID)
					artifact := domain.Artifact{
						Target:   sts.Target,
						Artifact: saveLocal.ArtifactPath,
					}

					err = saveartifactlocally.SaveArtifactLocally(
						ctx, exportCoordinator, b.opt.Log, artifact, artifactDir, saveLocal.DestPath, sts.ID, saveLocal.IfExists,
					)
					if err != nil {
						return nil, err
					}

					dirIndex++
				}
			}

			if !sts.GetDoSaves() || !sts.RunPush.HasState {
				continue
			}

			if opt.Push {
				for _, saveLocal := range sts.RunPush.SaveLocals {
					var outDir string

					outDir, err = b.tempEarthOutDir()
					if err != nil {
						return nil, err
					}

					dirID, ok := dirIDs[dirIndex]
					if !ok {
						return nil, fmt.Errorf("failed to map dir index %d", dirIndex)
					}

					artifactDir := filepath.Join(outDir, "index-"+dirID)
					artifact := domain.Artifact{
						Target:   sts.Target,
						Artifact: saveLocal.ArtifactPath,
					}

					err = saveartifactlocally.SaveArtifactLocally(
						ctx, exportCoordinator, b.opt.Log, artifact, artifactDir, saveLocal.DestPath, sts.ID, saveLocal.IfExists,
					)
					if err != nil {
						return nil, err
					}

					dirIndex++
				}

				continue
			}

			for _, commandStr := range sts.RunPush.CommandStrs {
				pushConsole.Printf("Did not execute push command %s\n", commandStr)
			}

			for _, saveImage := range sts.RunPush.SaveImages {
				pushConsole.Printf(
					"Did not push image %s as evaluating the image would "+
						"have caused a RUN --push to execute", saveImage.DockerTag,
				)
				outputConsole.Printf("Did not output image %s locally, "+
					"as evaluating the image would have caused a "+
					"RUN --push to execute", saveImage.DockerTag)
			}

			if sts.RunPush.InteractiveSession.Initialized {
				pushConsole.Printf("Did not start an %s interactive session "+
					"with command %s\n", sts.RunPush.InteractiveSession.Kind,
					sts.RunPush.InteractiveSession.CommandStr)
			}
		}
	}

	for _, artifactEntry := range exportCoordinator.GetArtifactSummary() {
		console := b.opt.Log.WithPrefixAndSalt(artifactEntry.Target, artifactEntry.Salt)
		targetStr := console.PrefixColor().Sprint(artifactEntry.Target)
		outputConsole.Printf("Artifact %s output as %s\n", targetStr, artifactEntry.Path)
	}

	for _, outputEntry := range exportCoordinator.GetLocalOutputSummary() {
		console := b.opt.Log.WithPrefixAndSalt(outputEntry.Target, outputEntry.Salt)
		targetStr := console.PrefixColor().Sprint(outputEntry.Target)
		outputConsole.Printf("Image %s output as %s\n", targetStr, outputEntry.DockerTag)
	}

	for _, pushEntry := range exportCoordinator.GetPushedImageSummary() {
		console := b.opt.Log.WithPrefixAndSalt(pushEntry.Target, pushEntry.Salt)

		targetStr := console.PrefixColor().Sprint(pushEntry.Target)
		if pushEntry.Pushed {
			pushConsole.Printf("Pushed image %s as %s\n", targetStr, pushEntry.DockerTag)
		} else {
			pushConsole.Printf("Did not push image %s\n", pushEntry.DockerTag)
		}
	}

	pushConsole.Flush()

	if opt.PrintPhases {
		b.opt.Log.PrintPhaseFooter(PhasePush)
		b.opt.Log.PrintPhaseHeader(PhaseOutput, !opt.Export.Artifacts(), outputPhaseSpecial)
	}

	outputConsole.Flush()

	for parentImageName, children := range manifestLists {
		err = dockerutil.
			LoadDockerManifest(ctx, b.opt.Log, b.opt.Engine, parentImageName, children, opt.PlatformResolver)
		if err != nil {
			return nil, err
		}
	}

	if opt.PrintPhases {
		b.opt.Log.PrintPhaseFooter(PhaseOutput)
		b.opt.Log.PrintSuccess()
	}

	return mts, nil
}

// builderImage is a SAVE IMAGE that builder.go exports, and what it does with it.
type builderImage struct {
	sts       *states.SingleTarget
	saveImage states.SaveImage
	plan      earthfile2llb.ImagePlan
	// multiPlatform is whether the image is one platform of a multi-platform
	// image: it is pushed as part of its tag's manifest list, and loaded locally
	// under a per-platform name.
	multiPlatform bool
	// member marks an image a wait block has already pushed or exported
	// locally, which only belongs in a manifest list builder.go makes; see
	// builderManifestListMembers. builder.go does not take its export, does
	// not load it again, and does not settle its outcome.
	member bool
}

// builderImagePlatform returns the platform img is exported for.
func builderImagePlatform(img builderImage) platutil.Platform {
	return img.sts.PlatformResolver.Materialize(img.sts.PlatformResolver.Current())
}

// builderImageManifest returns the per-platform local image img is loaded as.
func builderImageManifest(img builderImage) (dockerutil.Manifest, error) {
	platform := builderImagePlatform(img)

	name, err := llbutil.PlatformSpecificImageName(img.saveImage.DockerTag, platform)
	if err != nil {
		return dockerutil.Manifest{}, err
	}

	return dockerutil.Manifest{ImageName: name, Platform: platform}, nil
}

// takeBuilderImages returns the SAVE IMAGEs builder.go exports, in the order it
// exports them, and takes the export of each one (see
// states.ImageExport.TakeForBuilder). An image a wait block exports instead is
// left out, unless it belongs in one of the manifest lists builder.go makes
// (see builderManifestListMembers). cacheExport is whether the build exports a
// cache (--remote-cache).
func takeBuilderImages(
	opt BuildOpt,
	mts *states.MultiTarget,
	cacheExport bool,
	targetImages func(*states.SingleTarget) []states.SaveImage,
) ([]builderImage, error) {
	isMultiPlatform := make(map[string]struct{})    // DockerTag -> struct{}
	noManifestListImgs := make(map[string]struct{}) // DockerTag -> struct{}

	for _, sts := range mts.All() {
		if sts.PlatformResolver.Current() == platutil.DefaultPlatform {
			continue
		}

		for _, saveImage := range targetImages(sts) {
			doSaveOrPush := (sts.GetDoSaves() || sts.GetDoPushes() || saveImage.ForceSave)
			if !saveImage.BuilderSkips() && saveImage.DockerTag != "" && doSaveOrPush {
				if saveImage.NoManifestList {
					noManifestListImgs[saveImage.DockerTag] = struct{}{}
				} else {
					isMultiPlatform[saveImage.DockerTag] = struct{}{}
				}

				_, isMulti := isMultiPlatform[saveImage.DockerTag]
				_, noManifest := noManifestListImgs[saveImage.DockerTag]

				if isMulti && noManifest {
					return nil, fmt.Errorf(
						"cannot save image %s defined multiple times, but declared as SAVE IMAGE --no-manifest-list",
						saveImage.DockerTag,
					)
				}
			}
		}
	}

	var images []builderImage

	for _, sts := range mts.All() {
		for _, saveImage := range targetImages(sts) {
			plan := planImage(opt, sts, sts == mts.Final, saveImage)
			if !plan.SolvedByBuilder(saveImage, cacheExport) {
				// Short-circuit.
				continue
			}

			if !saveImage.Export.TakeForBuilder() {
				// A wait block exports it.
				continue
			}

			_, isMulti := isMultiPlatform[saveImage.DockerTag]

			images = append(images, builderImage{
				sts:           sts,
				saveImage:     saveImage,
				plan:          plan,
				multiPlatform: isMulti,
			})
		}
	}

	return append(images, builderManifestListMembers(mts, images, targetImages)...), nil
}

// builderManifestListMembers returns the images that belong in the manifest
// lists images make, but that a wait block has already pushed or exported
// locally, as members (see builderImage.member).
//
// They are platforms builder.go would have exported itself (SkipBuilder ==
// false: their SAVE IMAGE was left to it under --use-inline-cache), until a
// WAIT ... END that BUILT them too took their export over. builder.go pushes a
// multi-platform tag as one manifest list, which replaces what the registry
// had under the tag, and makes the local image from the per-platform images it
// is given. So those platforms stay in both: a member is pushed again from the
// ref the wait block solved, but it is not loaded again.
func builderManifestListMembers(
	mts *states.MultiTarget, images []builderImage, targetImages func(*states.SingleTarget) []states.SaveImage,
) []builderImage {
	type platformKey struct {
		tag, platform string
	}

	var (
		pushTags, localTags = map[string]bool{}, map[string]bool{}
		pushed, local       = map[platformKey]bool{}, map[platformKey]bool{}
	)

	for _, img := range images {
		if !img.multiPlatform {
			continue
		}

		key := platformKey{img.saveImage.DockerTag, builderImagePlatform(img).String()}

		if img.plan.Push {
			pushTags[key.tag] = true
			pushed[key] = true
		}

		if img.plan.Export {
			localTags[key.tag] = true
			local[key] = true
		}
	}

	succeeded := func(o *states.ExportOutcome) bool {
		done, err := o.Result()
		return done && err == nil
	}

	var members []builderImage

	for _, sts := range mts.All() {
		if sts.PlatformResolver.Current() == platutil.DefaultPlatform {
			continue
		}

		for _, saveImage := range targetImages(sts) {
			if saveImage.SkipBuilder || saveImage.NoManifestList || !saveImage.Export.TakenByWaitBlock() {
				continue
			}

			member := builderImage{sts: sts, saveImage: saveImage, multiPlatform: true, member: true}
			key := platformKey{saveImage.DockerTag, builderImagePlatform(member).String()}

			member.plan = earthfile2llb.ImagePlan{
				Push:   pushTags[key.tag] && !pushed[key] && succeeded(&saveImage.Export.Pushed),
				Export: localTags[key.tag] && !local[key] && succeeded(&saveImage.Export.ExportedLocally),
			}

			if !member.plan.Push && !member.plan.Export {
				continue
			}

			pushed[key] = pushed[key] || member.plan.Push
			local[key] = local[key] || member.plan.Export

			members = append(members, member)
		}
	}

	return members
}

func (b *Builder) targetPhaseState(sts *states.SingleTarget) pllb.State {
	if b.builtMain {
		return sts.RunPush.State
	}

	return sts.MainState
}

func (b *Builder) targetPhaseArtifacts(sts *states.SingleTarget) []states.SaveLocal {
	if b.builtMain {
		return sts.RunPush.SaveLocals
	}

	return sts.SaveLocals
}

func (b *Builder) targetPhaseImages(sts *states.SingleTarget) []states.SaveImage {
	if b.builtMain {
		return sts.RunPush.SaveImages
	}

	return sts.SaveImages
}

func (b *Builder) targetPhaseInteractiveSession(sts *states.SingleTarget) states.InteractiveSession {
	if b.builtMain {
		return sts.RunPush.InteractiveSession
	}

	return sts.InteractiveSession
}

func (b *Builder) stateToRef(
	ctx context.Context, gwClient gwclient.Client, state pllb.State, platr *platutil.Resolver,
) (gwclient.Reference, error) {
	noCache := b.opt.NoCache && !b.builtMain

	return llbutil.StateToRef(
		ctx, gwClient, state, noCache,
		platr, b.opt.CacheImports.AsSlice(),
	)
}

func (b *Builder) artifactStateToRef(
	ctx context.Context, gwClient gwclient.Client, state pllb.State, platr *platutil.Resolver,
) (gwclient.Reference, error) {
	noCache := b.opt.NoCache || b.builtMain

	return llbutil.StateToRef(
		ctx, gwClient, state, noCache,
		platr, b.opt.CacheImports.AsSlice(),
	)
}

func (b *Builder) tempEarthOutDir() (string, error) {
	var err error

	b.outDirOnce.Do(func() {
		tmpParentDir := ".tmp-earth-out"

		err = os.MkdirAll(tmpParentDir, 0o755) // #nosec G301
		if err != nil {
			err = fmt.Errorf("unable to create dir %s: %w", tmpParentDir, err)
			return
		}

		b.outDir, err = os.MkdirTemp(tmpParentDir, "tmp")
		if err != nil {
			err = fmt.Errorf("mk temp dir for artifacts: %w", err)
			return
		}

		b.opt.CleanCollection.Add(func() error {
			remErr := os.RemoveAll(b.outDir)
			// Remove the parent dir only if it's empty.
			_ = os.Remove(tmpParentDir)

			return remErr
		})
	})

	return b.outDir, err
}
