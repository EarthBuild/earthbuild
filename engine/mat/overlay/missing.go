package overlay

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// missingElement is the refusal when a step's base names something this store
// does not have.
//
// **With the room the store has, because that is usually the reason.** A worker
// whose filesystem is full empties its store trying to reach a free-space
// target it cannot reach, and the step that was about to use those layers then
// fails here - on the driver's console, on another machine, with nothing to
// connect the two. The fleet reads as broken and the disk reads as fine (E-F1).
//
// Reported rather than thresholded: any rule for when free space is "low enough
// to mention" is wrong on somebody's machine, and it is the next question a
// reader of this message asks in every case.
//
// `freeErr` non-nil means the filesystem could not be asked, and then the
// figure is left out rather than guessed - an invented zero would read as a
// full disk on a store with plenty of room.
func missingElement(id ir.NodeID, layerPath, declPath string, free uint64, freeErr error) error {
	room := ""
	if freeErr == nil {
		room = fmt.Sprintf("\n  the filesystem holding this store has %s free,"+
			" and a store collected down to nothing is what a full disk looks"+
			" like from here", human(free))
	}

	return fmt.Errorf(
		"%v is in this step's base and this store holds neither a layer nor a"+
			" declaration for it\n  looked for %s and %s\n  a base is materialised"+
			" from what the store has, so the element has to be fetched before the"+
			" step can run%s",
		id, layerPath, declPath, room)
}

// human is a byte count somebody can read.
//
// Its own rather than the store package's: `engine/store` imports this package,
// so this one cannot import it back.
func human(n uint64) string {
	const unit = 1024

	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := uint64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.3g %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
