package overlay

import (
	"fmt"
	"path/filepath"
	"strings"
)

// The names overlayfs gives a deletion, which are a *format* rather than a
// syscall: a layer built on any platform carries them, and reading one is not a
// linux-only act even though acting on it is. They live here, in the file with no
// build tag, so the rule about what a marker may name can be tested anywhere.
const (
	whPrefix = ".wh."
	whOpaque = ".wh..wh..opq"
)

// whiteoutTarget is the name a `.wh.` marker deletes.
//
// **A marker names a sibling, and only a sibling.** Overlayfs spells a deletion
// as `.wh.<name>` beside where `<name>` would be, so stripping the prefix must
// leave one ordinary path component. It did not have to: the name comes out of a
// layer archive, and `.wh...` strips to `..`, which `filepath.Join` then resolves
// to the *parent* of the directory being translated. The engine went on to
// `Mknod` that path - outside the destination - and the build failed with
// whatever the kernel said about it (gosec G703, E630).
//
// Nothing escaped, because the parent exists and `Mknod` refuses an existing
// path. That is luck rather than design: the check the code relied on was the
// filesystem's, the diagnostic named neither the layer nor the marker, and a
// destination whose parent happened not to exist would have had a device node
// written beside it.
//
// So the shape is asserted here instead: one component, not empty, not `.`, not
// `..`, no separator. Pure and platform-neutral on purpose - the syscalls are
// linux-only and this is the part worth testing everywhere.
func whiteoutTarget(marker string) (string, error) {
	name, ok := strings.CutPrefix(marker, whPrefix)
	if !ok {
		return "", fmt.Errorf("layer entry %q is not a whiteout marker", marker)
	}

	switch {
	case name == "":
		return "", fmt.Errorf("layer entry %q is a whiteout for nothing", marker)

	case name == "." || name == "..":
		return "", fmt.Errorf(
			"layer entry %q is a whiteout for %q, which is a directory reference"+
				" rather than a name in this directory",
			marker, name)

	case strings.ContainsRune(name, filepath.Separator):
		return "", fmt.Errorf(
			"layer entry %q is a whiteout for %q, and a marker names a sibling"+
				" rather than a path", marker, name)
	}

	return name, nil
}
