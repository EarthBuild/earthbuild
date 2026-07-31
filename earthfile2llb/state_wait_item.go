package earthfile2llb

import (
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
)

type stateWaitItem struct {
	c     *Converter
	state *pllb.State
}

// SetDoPush has no effect, but exists to satisfy interface.
func (swi *stateWaitItem) SetDoPush() {
}

// SetDoSave has no effect, but exists to satisfy interface.
func (swi *stateWaitItem) SetDoSave() {
}

func newStateWaitItem(state *pllb.State, c *Converter) states.WaitItem {
	return &stateWaitItem{
		c:     c,
		state: state,
	}
}
