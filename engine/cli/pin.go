package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/pin"
)

// Pin writes the digest of every image reference into the Earthfile.
//
// Not a build: nothing is planned, nothing is executed, and no sandbox is
// started. It resolves the references the file names and edits the file, which
// is the one thing in this engine that changes a file the user wrote - so it
// happens only when asked for by name, never as a side effect of building.
//
// `image:tag@sha256:...`, keeping the tag. The tag is what a reader recognises
// and what renovate's docker datasource matches on, so both halves stay
// maintained; the digest is what makes the build reproducible and what lets
// planning skip the registry entirely - 0.60s against 0.03s (E534).
//
// A reference that cannot be resolved is left as written and reported. An
// unreachable registry means a file this could not improve, which is the same
// trade the resolver makes during a build, and not a file it damaged.
func Pin(o Options) error {
	out := io.Writer(io.Discard)
	if o.Out != nil {
		out = o.Out
	}

	at := filepath.Join(o.Dir, "Earthfile")

	src, err := os.ReadFile(at) //nolint:gosec // the path the caller named
	if err != nil {
		return fmt.Errorf("read the Earthfile to pin: %w", err)
	}

	ctx := context.Background()

	challenges, err := imageCacheDir()
	if err != nil {
		challenges = ""
	}

	pinned, changes, err := pin.Rewrite(src, func(ref string) (string, error) {
		to, resolveErr := image.Resolve(ctx, ref, image.Options{
			Platform: resolveFor(o.Platform), Challenges: challenges,
		})
		if resolveErr != nil {
			return "", resolveErr
		}

		return pin.WithDigest(ref, to)
	})
	if err != nil {
		return fmt.Errorf("pin %s: %w", at, err)
	}

	done := 0

	for _, c := range changes {
		if c.Err != nil {
			fmt.Fprintf(out, "  %s:%d not pinned: %v\n", at, c.Line, c.Err)

			continue
		}

		fmt.Fprintf(out, "  %s:%d %s -> %s\n", at, c.Line, c.From, c.To)

		done++
	}

	if done == 0 {
		fmt.Fprintln(out, "  nothing to pin")

		return nil
	}

	// Written through a temporary file in the same directory and renamed, so an
	// interrupted pin leaves the Earthfile as it was rather than half of one.
	tmp, err := os.CreateTemp(o.Dir, ".Earthfile-pin-")
	if err != nil {
		return fmt.Errorf("stage the pinned Earthfile: %w", err)
	}

	_, err = tmp.Write(pinned)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("write the pinned Earthfile: %w", err)
	}

	err = tmp.Close()
	if err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("write the pinned Earthfile: %w", err)
	}

	// The mode the file already had: this edits somebody's file and has no
	// business changing what may read it.
	fi, err := os.Stat(at)
	if err == nil {
		_ = os.Chmod(tmp.Name(), fi.Mode().Perm())
	}

	err = os.Rename(tmp.Name(), at)
	if err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("replace %s with the pinned version: %w", at, err)
	}

	return nil
}
