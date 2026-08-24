package exec

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every mtime the engine writes goes through the clamp, or says why it does not.
//
// This reads the source, which is a blunt instrument, and it is used here
// because the property is about *where code is* rather than what it computes.
// Three times now a second piece of copying code has appeared beside the first
// and quietly disagreed with it about timestamps - `SAVE ARTIFACT` of a file
// against a directory, then `COPY` of a file against `COPY --dir` - and no
// behavioural test catches the fourth, because the fourth is a path nobody has
// thought to exercise yet. A test that catches it at the moment it is written
// is worth more than one that waits for somebody to reach it.
//
// The exception is real and is the reason this is a list rather than a ban:
// unpacking a downloaded image writes the times out of the tar header, and
// those are the upstream image's, not this build's. Clamping them would change
// layers this engine did not make and break the digests it just verified (I8).
func TestEveryMtimeIsClampedOrExcused(t *testing.T) {
	t.Parallel()

	// Files allowed to write a time that did not come through stamp(), and why.
	excused := map[string]string{
		"image/unpack.go": "the times belong to the image being unpacked, not to this build",
		// The index entry beside a layer, not the layer. Its mtime is the only
		// record of when a layer was last *read*, which is what lets a collector
		// drop last month's throwaway rather than the base image every build
		// starts from. Clamping it would stamp every entry with the build's
		// clamp and leave the collector ordering by a constant - the mechanism
		// still running and finding nothing, which is this project's most
		// recorded failure. Nothing digests it: it is bookkeeping, and no
		// layer's identity reaches it (E574).
		"store/index.go": "the time records when a layer was last read, which is" +
			" bookkeeping beside the layer rather than part of it",
		// The translator materialises a layer that already exists, turning its
		// portable deletion markers into what overlayfs reads (E94). Clamping
		// there would make the mounted view differ from the layer that was
		// digested - which is the I8 violation the clamp exists to prevent
		// everywhere else, arriving by the opposite route.
		"mat/overlay/whiteout_linux.go": "the times belong to the layer being materialised," +
			" and clamping them would make the mount disagree with the digest",
		// Unpacking a layer restores the times the layer *has*. They are part of
		// its identity (§3.3), so clamping them would produce a tree whose
		// digest is not the one that was asked for - the same I8 argument as the
		// row above, arriving from the transfer side (E262).
		"layer/unpack.go": "the times belong to the layer being restored, and" +
			" clamping them would restore a layer under a digest it does not have",
		// Placing one file of a base that a step is about to read. The time is
		// the one the layer records for it, and a step that stats a file it
		// faulted in must see what it would have seen had the whole base been
		// materialised (E290).
		"fleet/filler.go": "the time belongs to the layer the file was faulted" +
			" in from, and clamping it would make a lazily materialised base" +
			" differ from the same base materialised whole",
		// Placing a tree restores the times the *source* carries: a directory
		// is stamped after its contents, because creating them changed it, and
		// a recreated symlink has no time of its own to keep. Clamping either
		// would put the day of the placement into the layer's identity, which
		// is the defect this was written to remove (E545) - the same I8
		// argument as the rows above, arriving from the placement side.
		"store/place.go": "the times belong to the tree being placed, and" +
			" clamping them would name a layer by when it was placed rather" +
			" than by what it holds",
		// `clonefile(2)` copies times for files and links and not for
		// directories, so a cloned tree has to be told the ones its source
		// carried. Same argument as the row above, and the same defect: without
		// it a base image is named by the day it was cloned (E545).
		"exec/clone_darwin.go": "the times belong to the tree being cloned, and" +
			" clonefile does not copy a directory's own",
		// Putting back the time a directory had before this engine made a
		// mount point in it. Not a time being written, an edit being undone -
		// and a clamp here would set a value the lower layer does not have,
		// which is a difference invented rather than removed (E548).
		"guest/directoryasfound_linux.go": "the time is the one the directory" +
			" already had, restored after this engine borrowed the directory",
	}

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	found := 0

	err = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		// **A file that vanished is not a source file with an opinion about
		// mtimes.** `engine/store` generates a hundred-thousand-file fixture
		// into gitignored `testdata/` and renames it into place while this
		// walks the same tree, so a path can be listed and gone a moment later
		// (E616). Everything else still fails the walk: a source file this
		// cannot read is one the guard has not checked, and passing for that
		// reason is what the `found < 3` floor below is also about.
		if err != nil && errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		if err != nil {
			return err
		}

		if fi.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}

		b, err := os.ReadFile(p) //nolint:gosec // our own source tree
		if err != nil {
			return err
		}

		for i, line := range strings.Split(string(b), "\n") {
			// Every way the engine has of writing an mtime, not the one it had
			// when this was written. `lchtimes` arrived because os.Chtimes
			// follows a symlink and a link has an mtime of its own - a second
			// spelling, which a guard naming one function would not have seen.
			// Both take the time twice, so one rule covers both.
			// Matched without regard to case, because the spelling changed:
			// `lchtimes` was exported as `Lchtimes` when a second package
			// needed it, and a guard matching the lower-case name alone stopped
			// seeing every call the moment the rename landed. It is the same
			// hazard the paragraph above describes, arriving through the same
			// door twice - so the rule is now about the *name*, not about one
			// capitalisation of it.
			lower := strings.ToLower(line)
			if !strings.Contains(lower, "os.chtimes(") && !strings.Contains(lower, "lchtimes(") {
				continue
			}

			// The definition, not a call of it.
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "func lchtimes(") {
				continue
			}

			found++

			if why, ok := excused[filepath.ToSlash(rel)]; ok {
				t.Logf("%s:%d writes a time unclamped: %s", rel, i+1, why)

				continue
			}

			// The clamp is applied a line or two above the call, so the check is
			// on the argument: `at` is what stamp() returns everywhere it is
			// used, and a call passing anything else is a new rule.
			//
			// The times, not the path. The first version listed the permitted
			// *destination* variables - `(dst, at, at)`, `(target, at, at)` -
			// and so rejected a correct call whose path happened to be called
			// `p`. That is a coupling to a local name rather than to the
			// property, and it fails in the direction that teaches somebody to
			// rename a variable to satisfy a test. `os.Chtimes(x, time.Now(),
			// time.Now())` still fails, which is the rule this is for.
			if !strings.HasSuffix(strings.TrimSpace(line), ", at, at)") {
				t.Errorf("%s:%d writes an mtime that did not come through stamp():\n\t%s",
					rel, i+1, strings.TrimSpace(line))
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// A guard that reads no source is a guard that passes for the wrong reason -
	// a moved directory, a changed suffix, a walk that silently found nothing.
	if found < 3 {
		t.Errorf("only %d mtime writes were found in the engine, so this check is not reading it", found)
	}
}
