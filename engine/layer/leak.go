package layer

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Secret is a credential a step was given, by the name the Earthfile calls it.
//
// The value is here because finding it is the whole job. It must not travel any
// further than that: see Leak.
type Secret struct {
	Name  string
	Value string
}

// Leak is a secret found in something about to become a layer.
//
// **The value is deliberately not a field.** A finding is written to a build's
// output, and a report that quotes the credential has published it to every log
// that build feeds - which is the accident this exists to catch, committed by
// the thing catching it.
type Leak struct {
	// Path is where it was found, relative to the tree scanned.
	Path string
	// Name is the secret's id, as the Earthfile spells it.
	Name string
}

func (l Leak) String() string {
	return fmt.Sprintf("the secret %s appears in %s", l.Name, l.Path)
}

// leakChunk is how much of a file is held at once while scanning it.
const leakChunk = 64 << 10

// FindSecrets reports every place a secret's value appears under root.
//
// **A secret is mounted outside the step's filesystem so it cannot be captured,
// and then the step copies it**: `echo $TOKEN > /app/.env` puts the credential
// in the delta, the delta becomes a layer, and the layer is cached, exported
// and possibly pushed.
//
// Regular files only. A symlink has no contents of its own and a device is not
// a place a build writes a credential; a directory that cannot be read is
// skipped rather than fatal, because a scan that stops half way and reports
// nothing is worse than one that never ran.
//
// Every match is reported rather than the first, so a build that leaked a
// credential into four files is told about four and not asked to run again.
func FindSecrets(root string, secrets []Secret) ([]Leak, error) {
	live := make([]Secret, 0, len(secrets))

	for _, s := range secrets {
		if s.Value != "" {
			live = append(live, s)
		}
	}

	if len(live) == 0 {
		return nil, nil
	}

	// The longest value decides how much of one chunk has to be kept for the
	// next: a credential split across a read is still in the layer.
	keep := 0
	for _, s := range live {
		if len(s.Value) > keep {
			keep = len(s.Value)
		}
	}

	var found []Leak

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			// Unreadable, or nothing with contents. Neither is this function's
			// business and neither should end the walk.
			return nil //nolint:nilerr // see above: a skipped entry, not a failure
		}

		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}

		hits, serr := scanFile(path, live, keep)
		if serr != nil {
			return serr
		}

		for _, name := range hits {
			found = append(found, Leak{Path: rel, Name: name})
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s for secrets: %w", root, err)
	}

	return found, nil
}

// scanFile names every secret whose value appears in one file.
func scanFile(path string, secrets []Secret, keep int) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec // a path the caller is capturing anyway
	if err != nil {
		// Readable a moment ago and not now: a step's own file, gone. Not a
		// finding and not a failure.
		return nil, nil
	}

	defer f.Close()

	// One buffer, with room for the overlap that makes a split match findable.
	buf := make([]byte, leakChunk+keep)
	held := 0

	var hits []string

	seen := make(map[string]bool, len(secrets))

	for {
		n, rerr := f.Read(buf[held:])
		if n > 0 {
			window := buf[:held+n]

			for _, s := range secrets {
				if !seen[s.Name] && bytes.Contains(window, []byte(s.Value)) {
					seen[s.Name] = true

					hits = append(hits, s.Name)
				}
			}

			// Carry the tail forward, so a value straddling this read and the
			// next is whole in one window.
			if len(window) > keep {
				copy(buf, window[len(window)-keep:])
				held = keep
			} else {
				copy(buf, window)
				held = len(window)
			}
		}

		if rerr == io.EOF {
			break
		}

		if rerr != nil {
			return nil, fmt.Errorf("read %s: %w", path, rerr)
		}
	}

	return hits, nil
}
