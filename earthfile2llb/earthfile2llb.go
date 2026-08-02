// Package earthfile2llb converts parsed Earthfile ASTs into Buildkit Low-Level Builder (LLB) graphs for execution.
package earthfile2llb

import (
	"context"
	"fmt"
	"maps"

	"github.com/EarthBuild/earthbuild/buildcontext"
	"github.com/EarthBuild/earthbuild/buildcontext/provider"
	"github.com/EarthBuild/earthbuild/cleanup"
	"github.com/EarthBuild/earthbuild/cmd/earthly/bk"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/internal/telemetry"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/containerutil"
	"github.com/EarthBuild/earthbuild/util/gatewaycrafter"
	"github.com/EarthBuild/earthbuild/util/llbutil/secretprovider"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/util/syncutil/semutil"
	"github.com/EarthBuild/earthbuild/util/syncutil/serrgroup"
	"github.com/EarthBuild/earthbuild/variables"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/apicaps"
)

const commandName = "WITH DOCKER RUN "

// ProjectAdder provides an interface for adding projects.
type ProjectAdder interface {
	AddProject(org, proj string)
}

// ConvertOpt holds conversion parameters.
type ConvertOpt struct {
	MetaResolver                         llb.ImageMetaResolver
	BuildkitSkipper                      bk.BuildkitSkipper
	ProjectAdder                         ProjectAdder
	GwClient                             gwclient.Client
	DockerImageSolverTar                 states.DockerTarImageSolver
	MultiImageSolver                     states.MultiImageSolver
	ContainerFrontend                    containerutil.ContainerFrontend
	Visited                              states.VisitedCollection
	Parallelism                          semutil.Semaphore
	waitBlock                            *waitBlock
	InternalSecretStore                  *secretprovider.MutableMapStore
	BuildContextProvider                 *provider.BuildContextProvider
	OverridingVars                       *variables.Scope
	CacheImports                         *states.CacheImports
	OnExecutionSuccess                   func(context.Context)
	Resolver                             *buildcontext.Resolver
	FilesWithCommandRenameWarning        map[string]bool
	GlobalImports                        map[string]domain.ImportTrackerVal
	Logbus                               *logbus.Bus
	LLBCaps                              *apicaps.CapSet
	TempEarthOutDir                      func() (string, error)
	SolveCache                           *states.SolveCache
	LocalArtifactWhiteList               *gatewaycrafter.LocalArtifactWhiteList
	ExportCoordinator                    *gatewaycrafter.ExportCoordinator
	CleanCollection                      *cleanup.Collection
	TargetInputHashStackSet              map[string]bool
	parentDepSub                         chan string
	ErrorGroup                           *serrgroup.Group
	GitLookup                            *buildcontext.GitLookup
	LocalStateCache                      *LocalStateCache
	PlatformResolver                     *platutil.Resolver
	Features                             *features.Features
	Log                                  *conslogging.ConsoleLogger
	BuiltinArgs                          variables.DefaultArgs
	parentCommandID                      string
	parentTargetID                       string
	Runner                               string
	FeatureFlagOverrides                 string
	LocalRegistryAddr                    string
	ImageResolveMode                     llb.ResolveMode
	NoCache                              bool
	InteractiveDebuggerEnabled           bool
	IsCI                                 bool
	GlobalWaitBlockFtr                   bool
	DoSaves                              bool
	AllowPrivileged                      bool
	ForceSaveImage                       bool
	HasDangling                          bool
	InteractiveDebuggerDebugLevelLogging bool
	DoPushes                             bool
	OnlyFinalTargetImages                bool
	AllowInteractive                     bool
	AllowLocally                         bool
	UseLocalRegistry                     bool
	ParallelConversion                   bool
	UseFakeDep                           bool
	NoAutoSkip                           bool
	UseInlineCache                       bool
}

// Earthfile2LLB parses a earthfile and executes the statements for a given target.
func Earthfile2LLB(
	ctx context.Context, target domain.Target, opt ConvertOpt, initialCall bool,
) (mts *states.MultiTarget, retErr error) {
	ctx, span := telemetry.Tracer().Start(ctx, "+"+target.Target)
	defer span.End()

	if opt.SolveCache == nil {
		opt.SolveCache = states.NewSolveCache()
	}

	if opt.MetaResolver == nil {
		opt.MetaResolver = NewCachedMetaResolver(opt.GwClient)
	}

	if opt.TargetInputHashStackSet == nil {
		opt.TargetInputHashStackSet = make(map[string]bool)
	} else {
		// We are in a recursive call. Copy the stack set.
		newMap := make(map[string]bool, len(opt.TargetInputHashStackSet))
		maps.Copy(newMap, opt.TargetInputHashStackSet)
		opt.TargetInputHashStackSet = newMap
	}

	egWait := false

	if opt.ErrorGroup == nil {
		opt.ErrorGroup, ctx = serrgroup.WithContext(ctx)
		egWait = true

		defer func() {
			if retErr == nil {
				return
			}

			if egWait {
				// We haven't waited for the ErrorGroup yet. The ErrorGroup will
				// return the very first error encountered, which may be
				// different than what our error is (our error could be
				// context.Canceled resulted from the cancellation of the
				// ErrorGroup, but not the root cause).
				err2 := opt.ErrorGroup.Err()
				opt.Log.VerbosePrintf("earthfile2llb immediate error: %v", retErr)
				opt.Log.VerbosePrintf("earthfile2llb group error: %v", err2)

				if err2 != nil {
					retErr = err2
					return
				}
			}
		}()
	}
	// Resolve build context.
	bc, err := opt.Resolver.Resolve(ctx, opt.GwClient, opt.PlatformResolver, target)
	if err != nil {
		return nil, fmt.Errorf("resolve build context for target %s: %w", target.String(), err)
	}

	if opt.Visited == nil {
		if bc.Features.UseVisitedUpfrontHashCollection {
			opt.Visited = states.NewVisitedUpfrontHashCollection()
		} else {
			opt.Visited = states.NewLegacyVisitedCollection()
		}
	}

	opt.Features = bc.Features
	if initialCall && !bc.Features.ReferencedSaveOnly {
		opt.DoSaves = !target.IsRemote() // legacy mode only saves artifacts that are locally referenced
		opt.ForceSaveImage = true        // legacy mode always saves images regardless of locally or remotely referenced
	}

	opt.PlatformResolver.AllowNativeAndUser = opt.Features.NewPlatform

	if opt.waitBlock == nil {
		opt.waitBlock = newWaitBlock()
	}

	targetWithMetadata, ok := bc.Ref.(domain.Target)
	if !ok {
		return nil, fmt.Errorf("want domain.Target, got %T", bc.Ref)
	}

	sts, found, err := opt.Visited.
		Add(ctx, targetWithMetadata, opt.PlatformResolver, opt.AllowPrivileged, opt.OverridingVars, opt.parentDepSub)
	if err != nil {
		return nil, err
	}

	if opt.parentTargetID != "" {
		if parentTarget, ok := opt.Logbus.Run().Target(opt.parentTargetID); ok {
			parentTarget.AddDependsOn(sts.ID)
		}
	}

	if opt.parentCommandID != "" {
		if parentCmd, ok := opt.Logbus.Run().Command(opt.parentCommandID); ok {
			parentCmd.AddDependsOn(sts.ID, target.GetName())
		}
	}

	tiHash, err := sts.TargetInput().Hash()
	if err != nil {
		return nil, err
	}

	//nolint:nestif // TODO(jhorsts): simplify
	if found {
		if opt.TargetInputHashStackSet[tiHash] {
			return nil, fmt.Errorf("infinite cycle detected for target %s", target.String())
		}

		// Wait for the existing sts to complete first.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-sts.Done():
		}

		// The found target may have initially been created by a FROM or a COPY;
		// however, if it is referenced a second time by a BUILD, it may contain items that
		// require a save (export to the local host) or a push
		if opt.DoSaves {
			sts.SetDoSaves()
		}

		if opt.DoPushes {
			sts.SetDoPushes()
		}

		if opt.DoSaves || opt.DoPushes {
			err = sts.Wait(ctx)
			if err != nil {
				return nil, fmt.Errorf("wait failed on target %s: %w", target.String(), err)
			}
		}

		sts.AttachTopLevelWaitItems(ctx, opt.waitBlock)

		// This target has already been done.
		return &states.MultiTarget{
			Final:   sts,
			Visited: opt.Visited,
		}, nil
	}

	opt.TargetInputHashStackSet[tiHash] = true
	opt.Log.VerbosePrintf("earthfile2llb building %s with OverridingVars=%v",
		targetWithMetadata.StringCanonical(), opt.OverridingVars.Map())

	converter, err := NewConverter(targetWithMetadata, bc, sts, opt)
	if err != nil {
		return nil, err
	}

	interpreter := newInterpreter(
		converter,
		targetWithMetadata,
		opt.AllowPrivileged,
		opt.ParallelConversion,
		opt.Log,
		opt.GitLookup,
	)

	err = interpreter.Run(ctx, bc.Earthfile)
	if err != nil {
		return nil, err
	}

	mts, err = converter.FinalizeStates(ctx)
	if err != nil {
		return nil, err
	}

	if initialCall {
		err = opt.waitBlock.Wait(ctx, opt.DoPushes, opt.DoSaves)
		if err != nil {
			return nil, err
		}
	}

	if egWait {
		egWait = false

		err := opt.ErrorGroup.Wait()
		if err != nil {
			return nil, err
		}
	}

	return mts, nil
}
