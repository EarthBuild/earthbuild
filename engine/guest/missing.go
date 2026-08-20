package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// explainMissing says what was found where a path was expected.
//
// A path that never appeared has at least three causes with three different
// remedies: the image does not contain the file, the directory is not there at
// all because nothing was mounted, or the tree above it is missing because the
// store was never attached. The message that used to be printed - "the sandbox
// has no X to give this step" - is the same sentence for all three, which is
// why five WITH DOCKER failures across four Earthfiles are still unattributed
// (E28).
//
// Walks upwards to the first thing that does exist, because that is the
// boundary between what arrived and what did not, and it is the only part of
// the picture the caller cannot guess.
func explainMissing(path string) string {
	dir := filepath.Dir(path)

	entries, err := os.ReadDir(dir)
	if err == nil {
		return fmt.Sprintf("%s exists and holds %s", dir, summarise(entries))
	}

	// Upwards to the first directory that is there. Bounded by the root, which
	// every walk reaches.
	for at := dir; ; at = filepath.Dir(at) {
		parent := filepath.Dir(at)
		if parent == at {
			return fmt.Sprintf("%s does not exist, and neither does anything above it", dir)
		}

		_, err := os.Stat(parent)
		if err == nil {
			return fmt.Sprintf("%s does not exist; the nearest directory that does is %s", dir, parent)
		}
	}
}

// summarise names a few entries and counts the rest.
//
// Bounded because it goes into an error message: a directory listed in full is
// not a diagnosis, it is the reason people stop reading errors.
func summarise(entries []os.DirEntry) string {
	if len(entries) == 0 {
		return "nothing"
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	// Sorted, so the same directory reads the same way twice - a message that
	// changes between runs is one nobody can compare.
	sort.Strings(names)

	const show = 6
	if len(names) <= show {
		return fmt.Sprintf("%d entries: %s", len(names), strings.Join(names, ", "))
	}

	return fmt.Sprintf("%d entries, among them %s", len(names), strings.Join(names[:show], ", "))
}
