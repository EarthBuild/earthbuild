package earthfile2llb

import (
	"sync"

	"github.com/EarthBuild/earthbuild/states"
)

type saveImageWaitItem struct {
	c  *Converter
	si states.SaveImage

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

	// SetDoSave is what propagates local export down BUILD edges, so
	// --no-image-output has to be honoured here as well as at conversion time,
	// or a child target's image would be exported anyway.
	if siwi.c.opt.NoLocalImageExport {
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
