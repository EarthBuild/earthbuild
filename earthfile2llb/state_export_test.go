package earthfile2llb

import "github.com/EarthBuild/earthbuild/util/llbutil/pllb"

// isStateExported reports whether an image export is going to solve state
// anyway, or state needs no solving. It is Converter.exportOf as a bool, for
// tests.
func (c *Converter) isStateExported(state *pllb.State) bool {
	if state == nil || state.Output() == nil {
		return true
	}

	export := c.exportOf(state)

	return export.waitBlockItem != nil || export.byBuilder
}

// isStateExported reports whether this block's own Wait exports an image whose
// state is state, or state needs no solving. It is waitBlock.exportOf as a
// bool, for tests.
func (wb *waitBlock) isStateExported(state *pllb.State) bool {
	return exportsState(wb.imageExports(wb.snapshotItems()), state)
}
