package earthfile2llb

import (
	"sync"

	"github.com/EarthBuild/earthbuild/states"
)

type saveImageWaitItem struct {
	c *Converter
	// si.Export is shared with the target's SaveImages entry: it is how the wait
	// blocks and builder.go agree on who exports the image, and it is settled
	// once that export is done.
	si states.SaveImage

	allowPush   bool
	doPush      bool
	localExport bool

	// pushed and exportedLocally record what a wait block has already taken on.
	// The item is attached to every block that BUILDs its target, and none of
	// them may push or export it again.
	pushed          bool
	exportedLocally bool

	mu sync.Mutex
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
//
// The top-level block claims nothing while builder.go still exports the image.
// Any other block takes the export over from builder.go: it exports everything
// it holds by the time its Wait returns.
func (siwi *saveImageWaitItem) claim(topLevel bool) (doPush, localExport bool) {
	siwi.mu.Lock()
	defer siwi.mu.Unlock()

	doPush = siwi.doPush && !siwi.pushed
	localExport = siwi.localExport && !siwi.exportedLocally

	if !doPush && !localExport {
		return false, false
	}

	if topLevel && siwi.delegatedToBuilder() {
		return false, false
	}

	if !siwi.si.Export.TakeForWaitBlock() {
		return false, false
	}

	siwi.pushed = siwi.pushed || doPush
	siwi.exportedLocally = siwi.exportedLocally || localExport

	return doPush, localExport
}
