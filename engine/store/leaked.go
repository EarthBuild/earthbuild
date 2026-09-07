package store

import (
	"os"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// LeakedSuffix names the note beside a layer that holds a secret.
//
// Beside it rather than in it: the note is about the layer and must not change
// what the layer is, since the layer is named by its own contents.
const LeakedSuffix = ".leaked"

// NoteLeaked records that a layer holds a secret the build that made it was
// given.
//
// **A record kept in a process is forgotten by the next build.** The step that
// wrote the credential runs once; every build after it takes the layer from the
// cache, never runs the step, never scans, and knows nothing - so the second
// build would let out what the first was refused. The note therefore lives as
// long as the layer, the way `.unmarked` records what a capture learned.
//
// **Names and places, never values.** This file is as durable as the layer and
// would outlive every rotation of the credential it described.
//
// Best effort: a note that could not be written costs the check on a later
// build, which is where this engine was before the check existed.
func (d DirStore) NoteLeaked(id ir.NodeID, found []string) {
	if len(found) == 0 {
		return
	}

	sort.Strings(found)

	_ = os.WriteFile(d.LayerPath(id)+LeakedSuffix,
		[]byte(strings.Join(found, "\n")+"\n"), 0o600)
}

// LeakedIn is what was found in a layer, or nothing.
//
// Sorted, so two builds asking the same question are told the same thing in the
// same order (I12). A layer nobody has said anything about is clean, and asking
// is one failed stat.
func (d DirStore) LeakedIn(id ir.NodeID) []string {
	b, err := os.ReadFile(d.LayerPath(id) + LeakedSuffix)
	if err != nil {
		return nil
	}

	var out []string

	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}

	sort.Strings(out)

	return out
}
