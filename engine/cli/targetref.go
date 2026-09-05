package cli

import (
	"path/filepath"
	"strings"
)

// splitTargetRef separates a target reference into the directory it lives in
// and the target's own name.
//
// **`./dir+target` is the language's way of naming a target elsewhere**, and the
// interpreter has always resolved it - `targetRef` does so for every `BUILD`,
// `COPY` and `FROM` that crosses an Earthfile. Only the command line did not, so
// a reference that is ordinary inside a build was refused at the front door, and
// this repository's own corpus - which uses that form throughout - could not be
// driven by naming a target in it.
//
// The directory becomes the build's directory, because that is what the form
// means: the target is read from the Earthfile beside it, and its context is its
// own directory rather than the caller's.
//
// A reference with no `+` is not a reference and is returned untouched, which is
// what keeps `ls` and `doc` working.
func splitTargetRef(dir, ref string) (string, string) {
	at := strings.LastIndex(ref, "+")
	if at < 0 {
		return dir, ref
	}

	path, target := ref[:at], ref[at+1:]

	// `+target`, the ordinary form: no path before the separator, so the
	// directory stands and only the marker comes off.
	if path == "" {
		return dir, target
	}

	if filepath.IsAbs(path) {
		return filepath.Clean(path), target
	}

	return filepath.Join(dir, path), target
}
