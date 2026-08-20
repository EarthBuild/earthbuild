package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// caseSensitiveStore reports whether the layer store distinguishes Foo from foo.
//
// Asked of the store rather than inferred from the platform: a Mac may have a
// case-sensitive volume and a Linux machine a case-insensitive mount, and the
// question is about this directory.
//
// A store that cannot be probed is reported as insensitive, because the warning
// that follows is a warning and inventing the reassuring answer is the wrong way
// to be wrong.
func caseSensitiveStore(dir string) bool {
	sensitive, _ := probeCase(dir)

	return sensitive
}

// probeCase reports whether a directory is case-sensitive, and whether it could
// be asked at all.
//
// **Three outcomes, because there are three situations.** The first version had
// two and folded "could not tell" into "case-insensitive", so a Linux user's
// first build greeted them with a note about their ext4 store and a `hdiutil`
// command that does not exist there - the store had not been created yet, the
// probe's write failed with ENOENT, and absence became a negative answer.
//
// The same fault as treating a missing content digest as agreement (E81): a
// probe with fewer outcomes than the world silently rounds one of them to
// whichever answer is nearer.
func probeCase(dir string) (sensitive, known bool) {
	lower := filepath.Join(dir, ".earthbuild-case-probe")

	err := os.WriteFile(lower, []byte("l"), 0o600)
	if err != nil {
		return false, false
	}

	defer func() { _ = os.Remove(lower) }()

	upper := filepath.Join(dir, ".EARTHBUILD-CASE-PROBE")

	err = os.WriteFile(upper, []byte("u"), 0o600)
	if err != nil {
		return false, false
	}

	defer func() { _ = os.Remove(upper) }()

	b, err := os.ReadFile(lower) //nolint:gosec // a probe file this function just wrote
	if err != nil {
		return false, false
	}

	return string(b) == "l", true
}

// warnCaseInsensitive says what a case-insensitive store means for a build.
//
// A step's filesystem is an overlay: its *lower* layers are image layers from
// this store, and its upper layer lives inside the sandbox. On a case-
// insensitive store the two halves disagree - `/BIN/SH` resolves because it came
// from an image, while a file the step writes as `Foo` does not answer to `foo`.
//
// A build then behaves one way for files it was given and another for files it
// made. Most builds never notice; the ones that do are the ones that ask, and
// `examples/next-js` panics inside a TypeScript compiler doing exactly that.
//
// A warning rather than a refusal: nearly everything works, and refusing to
// build on a stock Mac would be refusing the common case to prevent an uncommon
// one.
// **Printed on a failure, not before one.**
//
// It was printed at the start of every build, which is where a five-line
// advisory ending in an `hdiutil create` command turns into the apparent
// diagnosis of whatever fails next. Twice it was not: once beside a guest that
// had not been built, once beside one built for the wrong platform, and three
// increments of work rested on a paragraph that was true and irrelevant (E491).
//
// A build that worked has nothing for it to explain. A build that failed may.
func caseNoteFor(dirs ...cacheDir) string {
	var b strings.Builder

	warnCaseInsensitive(&b, dirs...)

	return b.String()
}

func warnCaseInsensitive(w io.Writer, dirs ...cacheDir) {
	if w == nil {
		return
	}

	// Every directory an image is unpacked into, not only the store. The image
	// cache is the same directory by default and `EARTH_IMAGE_CACHE_DIR`
	// separates them, which is a sensible thing to do - an image is identical
	// for every project on the machine while a layer store belongs to one build
	// cache. Probing only the store meant a build with the store moved to a
	// case-sensitive volume still failed, and said "a case-sensitive volume for
	// the build cache is the way round it" while the build cache already was
	// one. A diagnosis naming a directory that is not at fault is worse than
	// none.
	seen := map[string]bool{}

	for _, d := range dirs {
		if d.path == "" || seen[d.path] {
			continue
		}

		// Silent unless the answer is known *and* bad. A store that has not
		// been created yet cannot be asked, and guessing is how this note came
		// to greet Linux users with a macOS command about an ext4 directory.
		sensitive, known := probeCase(d.path)
		if !known || sensitive {
			continue
		}

		seen[d.path] = true

		warnOne(w, d)
	}
}

// cacheDir is a directory images are unpacked into, and the variable that moves
// it.
//
// The variable travels with the path because the note ends in a command the
// reader is meant to run: naming the image cache as the problem and then
// telling them to move the build cache is a recipe for the wrong directory.
type cacheDir struct{ path, env string }

// warnOne says what a case-insensitive directory means for a build.
func warnOne(w io.Writer, d cacheDir) {
	dir := d.path

	fmt.Fprintf(w,
		"note: %s is on a case-insensitive filesystem\n"+
			"  image layers are read from there and a step's own writes are not, so paths from an\n"+
			"  image answer to any case and paths the build makes do not\n"+
			"  a case-sensitive volume for this directory removes the difference\n",
		dir)

	// Named after the store it replaces, so the commands can be run as printed
	// rather than adapted. Nothing here runs them: making a filesystem on
	// someone's machine is their decision, and a note is not consent.
	recipe := caseVolumeRecipe(filepath.Join(filepath.Dir(dir), "earthbuild-cache"),
		"/Volumes/EarthBuild", d.env)
	if len(recipe) == 0 {
		return
	}

	fmt.Fprintln(w, "  to make one:")

	for _, line := range recipe {
		fmt.Fprintf(w, "    %s\n", line)
	}
}

// CaseSensitive reports whether a directory distinguishes case, and whether the
// answer is known.
//
// Exported so a test can skip where there is nothing to say: on a case-sensitive
// volume the note never appears, and a test asserting it does would be asserting
// something about the machine (E491).
func CaseSensitive(dir string) (sensitive, known bool) {
	return probeCase(dir)
}
