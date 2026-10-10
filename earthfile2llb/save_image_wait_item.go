package earthfile2llb

import (
	"context"
	"sync"

	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/llbutil"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

type saveImageWaitItem struct {
	c *Converter

	// localManifestKey is the export coordinator key the image was exported
	// locally under, as one platform of a multi-platform image. It is set once
	// that export has succeeded, and lets a later Wait include the image in the
	// local multi-platform image it makes without loading it again.
	localManifestKey string

	// si.Export is shared with the target's SaveImages entry: it is how the wait
	// blocks and builder.go agree on who exports the image, and it is settled
	// once that export is done.
	si states.SaveImage

	mu sync.Mutex

	allowPush   bool
	doPush      bool
	localExport bool

	// pushed and exportedLocally record what a wait block has already taken on.
	// The item is attached to every block that BUILDs its target, and none of
	// them may push or export it again.
	pushed          bool
	exportedLocally bool
}

func newSaveImage(si states.SaveImage, c *Converter, allowPush, localExport bool) states.WaitItem {
	if si.Export == nil {
		si.Export = &states.ImageExport{}
	}

	return &saveImageWaitItem{
		c:           c,
		si:          si,
		allowPush:   allowPush,
		localExport: localExport,
	}
}

func (siwi *saveImageWaitItem) SetDoSave() {
	siwi.mu.Lock()
	defer siwi.mu.Unlock()

	// SetDoSave is what propagates local export down BUILD edges: it is called
	// when a BUILD reaches this target, which may be long after conversion decided
	// the target was unreferenced. So --no-image-output has to be honoured here as
	// well as at conversion time, or a child target's image would be exported
	// anyway.
	//
	// This asks Export, the user's intent for the whole build, and deliberately not
	// the per-target SaveReferenced: being referenced is precisely what this call
	// is announcing.
	if !siwi.c.opt.Export.Images() {
		return
	}

	if siwi.si.DockerTag != "" {
		siwi.localExport = true
	}
}

func (siwi *saveImageWaitItem) SetDoPush() {
	siwi.mu.Lock()
	defer siwi.mu.Unlock()

	if siwi.si.DockerTag != "" {
		siwi.doPush = siwi.allowPush
	}
}

// exportFlags returns whether the image is to be pushed and whether it is to be
// exported locally. Both are written by SetDoPush and SetDoSave, which a BUILD
// reaching an already-converted target can call at any time, so they are only
// ever read through here, under siwi.mu.
func (siwi *saveImageWaitItem) exportFlags() (doPush, localExport bool) {
	siwi.mu.Lock()
	defer siwi.mu.Unlock()

	return siwi.doPush, siwi.localExport
}

// delegatedToBuilder reports whether builder.go still exports this image: it was
// created in the top-level block under --use-inline-cache (SkipBuilder ==
// false), and no other wait block has taken its export over since.
func (siwi *saveImageWaitItem) delegatedToBuilder() bool {
	return !siwi.si.BuilderSkips()
}

// claim takes this image's export for one wait block's Wait, and returns what
// that Wait has to do: push it, export it locally, both or neither. What an
// earlier Wait (of this block or another) already took on is not done again.
// Instead, claim returns the outcomes of those exports in waitFor: the Wait
// must not return before they are done, because END promises that the image
// is pushed and loaded by then, and the Wait that took them on may still be
// running. A push is waited for as a push, and a local export as a local
// export: one being done says nothing about the other.
//
// The top-level block claims nothing while builder.go still exports the image,
// and waits for nothing: builder.go only runs after it. Any other block takes
// the export over from builder.go: it exports everything it holds by the time
// its Wait returns.
func (siwi *saveImageWaitItem) claim(topLevel bool) (doPush, localExport bool, waitFor []*states.ExportOutcome) {
	siwi.mu.Lock()
	defer siwi.mu.Unlock()

	needPush, needLocalExport := siwi.doPush, siwi.localExport

	if !needPush && !needLocalExport {
		return false, false, nil
	}

	if topLevel && siwi.delegatedToBuilder() {
		return false, false, nil
	}

	doPush = needPush && !siwi.pushed
	localExport = needLocalExport && !siwi.exportedLocally

	if (doPush || localExport) && !siwi.si.Export.TakeForWaitBlock() {
		// builder.go exports it. It only runs once the whole build has been
		// converted, after every Wait, so there is nothing to wait for.
		return false, false, nil
	}

	siwi.pushed = siwi.pushed || doPush
	siwi.exportedLocally = siwi.exportedLocally || localExport

	if needPush && !doPush {
		waitFor = append(waitFor, &siwi.si.Export.Pushed)
	}

	if needLocalExport && !localExport {
		waitFor = append(waitFor, &siwi.si.Export.ExportedLocally)
	}

	return doPush, localExport, waitFor
}

// pushedElsewhere reports whether the image is to be pushed, and an earlier
// Wait (of this block or another) has already taken that push on.
func (siwi *saveImageWaitItem) pushedElsewhere() bool {
	siwi.mu.Lock()
	defer siwi.mu.Unlock()

	return siwi.doPush && siwi.pushed
}

// exportedLocallyAs returns the export coordinator key an earlier Wait exported
// the image locally under, as one platform of a multi-platform image, or "" if
// none has (yet).
func (siwi *saveImageWaitItem) exportedLocallyAs() string {
	siwi.mu.Lock()
	defer siwi.mu.Unlock()

	if !siwi.localExport {
		return ""
	}

	return siwi.localManifestKey
}

// setLocalManifestKey records the export coordinator key the image has been
// exported locally under; see exportedLocallyAs.
func (siwi *saveImageWaitItem) setLocalManifestKey(key string) {
	siwi.mu.Lock()
	defer siwi.mu.Unlock()

	siwi.localManifestKey = key
}

// ref returns the image's solved state. It is solved once, however many
// exports it is part of; see states.ImageExport.Ref.
func (siwi *saveImageWaitItem) ref(ctx context.Context) (gwclient.Reference, error) {
	c := siwi.c

	return siwi.si.Export.Ref(ctx, func(ctx context.Context) (gwclient.Reference, error) {
		return llbutil.StateToRef(ctx, c.opt.GwClient, siwi.si.State, c.opt.NoCache, c.platr, c.opt.CacheImports.AsSlice())
	})
}
