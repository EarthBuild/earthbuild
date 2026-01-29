package builder

import (
	"context"
	"io"
	"maps"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/EarthBuild/earthbuild/logbus/solvermon"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/flagutil"
	"github.com/EarthBuild/earthbuild/util/fsutilprogress"

	"github.com/moby/buildkit/client"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/pullping"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/grpcerrors"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

// statusChanSize is used to ensure we consume all BK status messages without
// causing back-pressure that forces BK to cancel.
const statusChanSize = 500

type (
	onImageFunc         func(context.Context, *errgroup.Group, string, string, string) (io.WriteCloser, error)
	onArtifactFunc      func(context.Context, string, domain.Artifact, string, string) (string, error)
	onFinalArtifactFunc func(context.Context) (string, error)
)

type solver struct {
	logbusSM        *solvermon.SolverMonitor
	bkClient        *client.Client
	cacheImports    *states.CacheImports
	cacheExport     string
	maxCacheExport  string
	attachables     []session.Attachable
	enttlmnts       []entitlements.Entitlement
	saveInlineCache bool
}

func (s *solver) buildMainMulti(
	ctx context.Context,
	bf gwclient.BuildFunc,
	onImage onImageFunc,
	onArtifact onArtifactFunc,
	onFinalArtifact onFinalArtifactFunc,
	onPullCallback pullping.PullCallback,
	console conslogging.ConsoleLogger,
) error {
	ch := make(chan *client.SolveStatus, statusChanSize)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	eg, ctx := errgroup.WithContext(ctx)

	solveOpt, err := s.newSolveOptMulti(ctx, eg, onImage, onArtifact, onFinalArtifact, onPullCallback, console)
	if err != nil {
		return errors.Wrap(err, "new solve opt")
	}

	var buildErr error

	eg.Go(func() error {
		_, inErr := s.bkClient.Build(ctx, *solveOpt, "", bf, ch)
		if inErr != nil {
			if grpcErr, ok := grpcerrors.AsGRPCStatus(inErr); ok {
				interpreterErr := earthfile2llb.FromError(errors.New(grpcErr.Message()))
				if interpreterErr != nil {
					inErr = interpreterErr
				}
			}
			// The actual error from bkClient.Build sometimes races with
			// a context cancelled in the solver monitor.
			buildErr = inErr

			return inErr
		}

		return nil
	})
	eg.Go(func() error {
		return s.logbusSM.MonitorProgress(ctx, ch)
	})
	err = eg.Wait()

	if buildErr != nil {
		return buildErr
	}

	if err != nil {
		return err
	}

	return nil
}

func (s *solver) newSolveOptMulti(
	ctx context.Context,
	eg *errgroup.Group,
	onImage onImageFunc,
	onArtifact onArtifactFunc,
	onFinalArtifact onFinalArtifactFunc,
	onPullCallback pullping.PullCallback,
	console conslogging.ConsoleLogger,
) (*client.SolveOpt, error) {
	imports := s.cacheImports.AsSlice()

	cacheImports := make([]client.CacheOptionsEntry, 0, len(imports))
	for _, ci := range imports {
		cacheImports = append(cacheImports, newCacheImportOpt(ci))
	}

	var cacheExports []client.CacheOptionsEntry

	if s.cacheExport != "" {
		cacheExportName, attrs, err := flagutil.ParseImageNameAndAttrs(s.cacheExport)
		if err != nil {
			return nil, errors.Wrapf(err, "parse export cache error: %s", s.cacheExport)
		}

		cacheExports = append(cacheExports, newCacheExportOpt(cacheExportName, attrs, false))
	}

	if s.maxCacheExport != "" {
		maxCacheExportName, attrs, err := flagutil.ParseImageNameAndAttrs(s.maxCacheExport)
		if err != nil {
			return nil, errors.Wrapf(err, "parse max export cache error: %s", s.maxCacheExport)
		}

		cacheExports = append(cacheExports, newCacheExportOpt(maxCacheExportName, attrs, true))
	}

	if s.saveInlineCache {
		cacheExports = append(cacheExports, newInlineCacheOpt())
	}

	progressCB := fsutilprogress.New("", console.WithPrefix("output"))

	isTrue := func(s string) bool {
		return s == "true"
	}

	return &client.SolveOpt{
		Exports: []client.ExportEntry{
			{
				Type:  client.ExporterEarthly,
				Attrs: map[string]string{},
				Output: func(md map[string]string) (io.WriteCloser, error) {
					if !isTrue(md["export-image"]) {
						return nil, nil
					}

					imageName := md["image.name"]
					waitFor := md["export-image-wait-for"]
					manifestKey := md["export-image-manifest-key"]

					return onImage(ctx, eg, imageName, waitFor, manifestKey)
				},
				OutputDirFunc: func(md map[string]string) (string, error) {
					if !isTrue(md["export-dir"]) {
						// Use the other fun for images.
						return "", nil
					}

					if isTrue(md["final-artifact"]) {
						return onFinalArtifact(ctx)
					}

					indexStr := md["dir-id"]
					artifactStr := md["artifact"]
					srcPath := md["src-path"]
					destPath := md["dest-path"]

					artifact, err := domain.ParseArtifact(artifactStr)
					if err != nil {
						return "", errors.Wrapf(err, "parse artifact %s", artifactStr)
					}

					return onArtifact(ctx, indexStr, artifact, srcPath, destPath)
				},
				OutputPullCallback: onPullCallback,
				VerboseProgressCB:  progressCB.Verbose,
			},
		},
		CacheImports:        cacheImports,
		CacheExports:        cacheExports,
		Session:             s.attachables,
		AllowedEntitlements: s.enttlmnts,
	}, nil
}

func newCacheImportOpt(ref string) client.CacheOptionsEntry {
	registryCacheOptAttrs := make(map[string]string)
	registryCacheOptAttrs["ref"] = ref

	return client.CacheOptionsEntry{
		Type:  "registry",
		Attrs: registryCacheOptAttrs,
	}
}

func newCacheExportOpt(ref string, attrs map[string]string, maxMode bool) client.CacheOptionsEntry {
	registryCacheOptAttrs := make(map[string]string)
	registryCacheOptAttrs["ref"] = ref
	maps.Copy(registryCacheOptAttrs, attrs)

	if maxMode {
		registryCacheOptAttrs["mode"] = "max"
	}

	return client.CacheOptionsEntry{
		Type:  "registry",
		Attrs: registryCacheOptAttrs,
	}
}

func newInlineCacheOpt() client.CacheOptionsEntry {
	return client.CacheOptionsEntry{
		Type: "inline",
	}
}
