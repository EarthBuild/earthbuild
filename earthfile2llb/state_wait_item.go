package earthfile2llb

import (
	"github.com/earthbuild/earthbuild/util/llbutil/pllb"
	"github.com/earthbuild/earthbuild/util/waitutil"
)

type stateWaitItem struct {
	c     *Converter
	state *pllb.State
}

// SetDoPush has no effect, but exists to satisfy interface
func (swi *stateWaitItem) SetDoPush() {
}

// SetDoSave has no effect, but exists to satisfy interface
func (swi *stateWaitItem) SetDoSave() {
}

func newStateWaitItem(state *pllb.State, c *Converter) waitutil.WaitItem {
	return &stateWaitItem{
		c:     c,
		state: state,
	}
}
