package earthfile2llb

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/internal/telemetry"
	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/dockerutil"
	"github.com/EarthBuild/earthbuild/util/gatewaycrafter"
	"github.com/EarthBuild/earthbuild/util/llbutil"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/EarthBuild/earthbuild/util/saveartifactlocally"
	"github.com/EarthBuild/earthbuild/util/syncutil/semutil"
	"github.com/EarthBuild/earthbuild/util/syncutil/serrgroup"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

type waitBlock struct {
	// itemsMu guards seenItems and items. It is only ever held briefly, so a
	// caller that just wants to look at the items is never stuck behind a Wait
	// that is busy solving and exporting them.
	seenItems map[states.WaitItem]struct{}
	items     []states.WaitItem
	itemsMu   sync.Mutex
	// mu serialises Wait, and guards the short-circuit flags below. Wait holds
	// it for the whole solve and export.
	mu sync.Mutex
	// used for short-circuiting
	called            bool
	pushCalled        bool
	localExportCalled bool
	// topLevel marks the build's top-level implicit wait block: the root
	// target's, which every target BUILT outside any WAIT inherits. It is
	// waited on once, at the very end of conversion, right before builder.go
	// runs. So it is the only block that can hand an image to builder.go
	// without breaking the ordering an explicit WAIT ... END promises.
	topLevel bool
	// detached marks a block created for a target reached by FROM or COPY,
	// which pass no wait block. Nothing ever waits on it, so it exports nothing.
	detached bool
}

// imageExport is an image a wait block exports itself, with the flags it is
// exported with. The flags are read once per Wait, so that everything the Wait
// does acts on the same view of them.
type imageExport struct {
	*saveImageWaitItem

	doPush      bool
	localExport bool
}

func newWaitBlock() *waitBlock {
	return &waitBlock{
		seenItems: map[states.WaitItem]struct{}{},
	}
}

func (wb *waitBlock) SetDoSaves() {
	wb.itemsMu.Lock()
	defer wb.itemsMu.Unlock()

	for _, wi := range wb.items {
		wi.SetDoSave()
	}
}

func (wb *waitBlock) SetDoPushes() {
	wb.itemsMu.Lock()
	defer wb.itemsMu.Unlock()

	for _, wi := range wb.items {
		wi.SetDoPush()
	}
}

func (wb *waitBlock) AddItem(item states.WaitItem) {
	wb.itemsMu.Lock()
	defer wb.itemsMu.Unlock()

	_, exists := wb.seenItems[item]
	if exists {
		return
	}

	wb.seenItems[item] = struct{}{}
	wb.items = append(wb.items, item)
}

// delegatesToBuilder reports whether this block leaves item's export to
// builder.go instead of exporting it itself. Only the top-level block delegates.
// Any other block exports every item it holds by the time its Wait returns, even
// one created with SkipBuilder == false in the top-level block and attached here
// later, because END must not return before its images are exported.
func (wb *waitBlock) delegatesToBuilder(item *saveImageWaitItem) bool {
	return wb.topLevel && !item.si.SkipBuilder
}

// imageExports returns the images in items that this block's own Wait exports,
// with the flags they are exported with. It is the one filter for that question:
// saveImages exports exactly these, and waitStates and Converter.isStateExported
// use it to tell whether a state is already being solved by an export.
//
// An item the block leaves to builder.go is not included, and neither is any
// item of a detached block, which is never waited on.
func (wb *waitBlock) imageExports(items []states.WaitItem) []imageExport {
	var exports []imageExport

	for _, item := range items {
		saveImage, ok := item.(*saveImageWaitItem)
		if !ok {
			continue
		}

		if wb.detached || wb.delegatesToBuilder(saveImage) {
			continue
		}

		doPush, localExport := saveImage.exportFlags()
		if !doPush && !localExport {
			continue
		}

		exports = append(exports, imageExport{
			saveImageWaitItem: saveImage,
			doPush:            doPush,
			localExport:       localExport,
		})
	}

	return exports
}

// snapshotItems returns the items added so far. A Wait acts on the items
// present when it starts; an item added while it runs is left to a later Wait.
func (wb *waitBlock) snapshotItems() []states.WaitItem {
	wb.itemsMu.Lock()
	defer wb.itemsMu.Unlock()

	return slices.Clone(wb.items)
}

func (wb *waitBlock) Wait(ctx context.Context, push, localExport bool) error {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	shortCircuit := wb.called

	wb.called = true
	if push && !wb.pushCalled {
		shortCircuit = false
		wb.pushCalled = true
	}

	if localExport && !wb.localExportCalled {
		shortCircuit = false
		wb.localExportCalled = true
	}

	if shortCircuit {
		return nil
	}

	items := wb.snapshotItems()
	exports := wb.imageExports(items)

	errGroup, ctx := serrgroup.WithContext(ctx)
	errGroup.Go(func() error {
		return saveImages(ctx, exports)
	})

	if localExport {
		errGroup.Go(func() error {
			return wb.saveArtifactLocal(ctx, items)
		})
	}

	errGroup.Go(func() error {
		return wb.waitStates(ctx, items, exports)
	})

	return errGroup.Wait()
}

func saveImages(ctx context.Context, exports []imageExport) error {
	isMultiPlatform := make(map[string]bool)        // DockerTag -> bool
	noManifestListImgs := make(map[string]struct{}) // set based on DockerTag
	platformImgNames := make(map[string]bool)
	singPlatImgNames := make(map[string]bool) // ensure that these are unique

	for _, saveImage := range exports {
		if hasPlatform, ok := isMultiPlatform[saveImage.si.DockerTag]; ok {
			if saveImage.si.HasPlatform != hasPlatform {
				format := "SAVE IMAGE %s is defined multiple times, but not all commands defined a --platform value"
				return fmt.Errorf(format, saveImage.si.DockerTag)
			}

			if !hasPlatform {
				format := "SAVE IMAGE %s was already declared (none had --platform values)"
				return fmt.Errorf(format, saveImage.si.DockerTag)
			}

			if _, found := noManifestListImgs[saveImage.si.DockerTag]; found {
				format := "cannot save image %s defined multiple times, but declared as SAVE IMAGE --no-manifest-list"
				return fmt.Errorf(format, saveImage.si.DockerTag)
			}
		}

		if saveImage.si.HasPlatform {
			// SAVE IMAGE was called with a --platform value
			if saveImage.si.NoManifestList {
				noManifestListImgs[saveImage.si.DockerTag] = struct{}{}
				isMultiPlatform[saveImage.si.DockerTag] = false
			} else {
				isMultiPlatform[saveImage.si.DockerTag] = true // do I need to count for previously seen?
			}
		} else {
			isMultiPlatform[saveImage.si.DockerTag] = false
		}
	}

	if len(exports) == 0 {
		return nil
	}

	gwCrafter := gatewaycrafter.NewGatewayCrafter()

	// these are used to pass manifest data to the onImage function in builder.go;
	// this only applies to non-local-registry exports
	var (
		tarImagesInWaitBlockRefPrefixes []string
		tarImagesInWaitBlock            []string
	)

	refID := 0

	for _, item := range exports {
		sessionID := item.c.opt.GwClient.BuildOpts().SessionID
		exportCoordinator := item.c.opt.ExportCoordinator

		ref, err := llbutil.StateToRef(
			ctx, item.c.opt.GwClient, item.si.State, item.c.opt.NoCache,
			item.c.platr, item.c.opt.CacheImports.AsSlice(),
		)
		if err != nil {
			return fmt.Errorf("failed to solve image required for %s: %w", item.si.DockerTag, err)
		}

		var (
			platformBytes   []byte
			platformImgName string
		)

		//nolint:nestif // TODO(jhorsts): simplify
		if isMultiPlatform[item.si.DockerTag] {
			platformBytes = []byte(item.si.Platform.String())

			platformImgName, err = llbutil.PlatformSpecificImageName(item.si.DockerTag, item.si.Platform)
			if err != nil {
				return err
			}

			if item.si.CheckDuplicate && item.si.DockerTag != "" {
				if _, found := platformImgNames[platformImgName]; found {
					return fmt.Errorf(
						"image %s is defined multiple times for the same platform (%s)",
						item.si.DockerTag, item.si.Platform.String(),
					)
				}

				platformImgNames[platformImgName] = true
			}
		} else if item.si.CheckDuplicate && item.si.DockerTag != "" {
			if _, found := singPlatImgNames[item.si.DockerTag]; found {
				return fmt.Errorf(
					"image %s is defined multiple times for the same default platform",
					item.si.DockerTag,
				)
			}

			singPlatImgNames[item.si.DockerTag] = true
		}

		refPrefix, err := gwCrafter.AddPushImageEntry(
			ref, refID, item.si.DockerTag, item.doPush, item.si.InsecurePush, item.si.Image, platformBytes,
		)
		if err != nil {
			return err
		}

		refID++

		if item.localExport {
			switch {
			case isMultiPlatform[item.si.DockerTag]:
				// local docker instance does not support multi-platform images, so we must create a new entry
				// and set it to the platformImgName
				refPrefix, err = gwCrafter.AddPushImageEntry(ref, refID, platformImgName, false, false, item.si.Image, nil)
				if err != nil {
					return err
				}

				exportCoordinatorImageID := exportCoordinator.AddImage(sessionID, item.si.DockerTag, &dockerutil.Manifest{
					ImageName: platformImgName,
					Platform:  item.si.Platform,
				})

				if item.c.opt.UseLocalRegistry {
					gwCrafter.AddMeta(refPrefix+"/export-image-local-registry", []byte(exportCoordinatorImageID))
				} else {
					gwCrafter.AddMeta(refPrefix+"/export-image", []byte("true"))
					gwCrafter.AddMeta(refPrefix+"/export-image-manifest-key", []byte(exportCoordinatorImageID))
					tarImagesInWaitBlockRefPrefixes = append(tarImagesInWaitBlockRefPrefixes, refPrefix)
					tarImagesInWaitBlock = append(tarImagesInWaitBlock, exportCoordinatorImageID)
				}

				refID++
			case item.c.opt.UseLocalRegistry:
				exportCoordinatorImageID := exportCoordinator.AddImage(sessionID, item.si.DockerTag, nil)
				gwCrafter.AddMeta(refPrefix+"/export-image-local-registry", []byte(exportCoordinatorImageID))
			default:
				gwCrafter.AddMeta(refPrefix+"/export-image", []byte("true"))
			}

			exportCoordinator.AddLocalOutputSummary(item.c.target.String(), item.si.DockerTag, item.c.mts.Final.ID)
		}
	}

	if len(tarImagesInWaitBlockRefPrefixes) != 0 {
		waitFor := strings.Join(tarImagesInWaitBlock, " ")
		// the wait-for entry is used to know when all multiplatform images have been exported,
		// thus making it safe to load manifests
		for _, refPrefix := range tarImagesInWaitBlockRefPrefixes {
			gwCrafter.AddMeta(refPrefix+"/export-image-wait-for", []byte(waitFor))
		}
	}

	if len(exports) == 0 {
		panic("saveImagesWaitItem should never have been created with zero converters")
	}

	gatewayClient := exports[0].c.opt.GwClient // could be any converter's gwClient (they should app be the same)

	refs, metadata := gwCrafter.GetRefsAndMetadata()

	err := gatewayClient.Export(ctx, gwclient.ExportRequest{
		Refs:     refs,
		Metadata: metadata,
	})
	if err != nil {
		return fmt.Errorf("failed to SAVE IMAGE: %w", err)
	}

	return nil
}

// waitStates force executes the states in items, except those an image export
// solves anyway: an image in exports, which this same Wait exports alongside, or,
// for the top-level block only, an image builder.go exports right after it.
func (wb *waitBlock) waitStates(ctx context.Context, items []states.WaitItem, exports []imageExport) error {
	stateItems := []*stateWaitItem{}

	for _, item := range items {
		stateItem, ok := item.(*stateWaitItem)
		if !ok {
			continue
		}

		if exportsState(exports, stateItem.state) {
			continue
		}

		if wb.topLevel && stateItem.c.builderExportsState(stateItem.state) {
			continue
		}

		stateItems = append(stateItems, stateItem)
	}

	if len(stateItems) == 0 {
		return nil
	}

	// all converters have the same semaphore
	sharedParallelism := stateItems[0].c.opt.Parallelism

	// This semaphore ensures that there is at least one thread allowed to progress,
	// even if parallelism is completely starved.
	sem := semutil.NewMultiSem(sharedParallelism, semutil.NewWeighted(1))

	errGroup, ctx := serrgroup.WithContext(ctx)

	for _, item := range stateItems {
		errGroup.Go(func() error {
			rel, err := sem.Acquire(ctx, 1)
			if err != nil {
				return fmt.Errorf("acquiring parallelism semaphore during waitStates for %s: %w", item.c.target.String(), err)
			}
			defer rel()

			return item.c.forceExecution(ctx, *item.state, item.c.platr)
		})
	}

	return errGroup.Wait()
}

// isStateExported reports whether this block's own Wait exports an image whose
// state is state, so that solving state separately would solve the same vertex
// twice. Images the block leaves to builder.go do not count here; see
// Converter.isStateExported.
//
// It only takes itemsMu, never mu, so it does not wait for a Wait in progress.
func (wb *waitBlock) isStateExported(state *pllb.State) bool {
	return exportsState(wb.imageExports(wb.snapshotItems()), state)
}

// exportsState reports whether one of exports is an image whose state is state.
// A missing or scratch state needs no solving, so it counts as exported.
func exportsState(exports []imageExport, state *pllb.State) bool {
	if state == nil || state.Output() == nil {
		return true
	}

	for _, export := range exports {
		if export.si.State.Output() == state.Output() {
			return true
		}
	}

	return false
}

type saveArtifactLocalEntry struct {
	artifact    domain.Artifact
	artifactDir string
	destPath    string
	salt        string
	ifExists    bool
}

func (wb *waitBlock) saveArtifactLocal(ctx context.Context, items []states.WaitItem) error {
	ctx, span := telemetry.Tracer().Start(ctx, "SAVE ARTIFACT AS LOCAL")
	defer span.End()

	gwCrafter := gatewaycrafter.NewGatewayCrafter()

	var (
		gatewayClient     gwclient.Client
		log               *conslogging.ConsoleLogger
		exportCoordinator *gatewaycrafter.ExportCoordinator
		artifacts         []saveArtifactLocalEntry
		localDestinations []string
	)

	for refID, item := range items {
		saveLocalItem, ok := item.(*saveArtifactLocalWaitItem)
		if !ok {
			continue
		}

		c := saveLocalItem.c
		i := saveLocalItem.saveLocal.Index
		gatewayClient = c.opt.GwClient
		log = c.opt.Log
		exportCoordinator = c.opt.ExportCoordinator

		state := c.mts.Final.SeparateArtifactsState[i]

		ref, err := llbutil.StateToRef(ctx, c.opt.GwClient, state, c.opt.NoCache, c.platr, c.opt.CacheImports.AsSlice())
		if err != nil {
			return err
		}

		artifact := domain.Artifact{
			Target:   c.target,
			Artifact: saveLocalItem.saveLocal.ArtifactPath,
		}

		dirID, err := gwCrafter.AddSaveArtifactLocal(
			ref, refID, artifact.String(), saveLocalItem.saveLocal.ArtifactPath, saveLocalItem.saveLocal.DestPath,
		)
		if err != nil {
			return err
		}

		c.opt.LocalArtifactWhiteList.Add(saveLocalItem.saveLocal.DestPath)

		outDir, err := c.opt.TempEarthOutDir()
		if err != nil {
			return err
		}

		artifacts = append(artifacts, saveArtifactLocalEntry{
			artifact:    artifact,
			artifactDir: filepath.Join(outDir, "index-"+dirID),
			destPath:    saveLocalItem.saveLocal.DestPath,
			ifExists:    saveLocalItem.saveLocal.IfExists,
			salt:        c.mts.Final.ID,
		})

		localDestinations = append(localDestinations, saveLocalItem.saveLocal.DestPath)
	}

	span.SetAttributes(semconv.ArtifactLocalDestinations.StringSlice(localDestinations))

	refs, metadata := gwCrafter.GetRefsAndMetadata()
	if len(refs) == 0 {
		if len(metadata) != 0 {
			panic("metadata should always be empty when refs is empty")
		}

		return nil
	}

	err := gatewayClient.Export(ctx, gwclient.ExportRequest{
		Refs:     refs,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}

	for _, entry := range artifacts {
		err = saveartifactlocally.SaveArtifactLocally(
			ctx, exportCoordinator, log, entry.artifact, entry.artifactDir, entry.destPath, entry.salt, entry.ifExists,
		)
		if err != nil {
			return err
		}
	}

	return nil
}
