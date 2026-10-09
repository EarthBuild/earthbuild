package earthfile2llb

import (
	"sync"

	"github.com/EarthBuild/earthbuild/states"
)

type saveImageWaitItem struct {
	c  *Converter
	si states.SaveImage

	// exported is settled by the first wait block export that includes this
	// image.
	exported states.ExportOutcome

	allowPush   bool
	doPush      bool
	localExport bool

	mu sync.Mutex
}

func newSaveImage(si states.SaveImage, c *Converter, allowPush, localExport bool) states.WaitItem {
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
