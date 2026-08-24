package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// TestCorpusTargetsActuallyBuild runs corpus targets rather than planning them.
//
// The corpus test proves an Earthfile *plans*. This session proved four times
// over that planning is not building: `COPY x .` planned perfectly and failed
// in the guest, `WORKDIR` + `COPY` planned perfectly and put files at the
// filesystem root, a step had no /dev, and ENV took PATH with it. Every one of
// those produced a flawless plan.
//
// So this is the other half of the measurement, and it is a *measurement* -
// it reports what fraction of the corpus runs and names what did not, rather
// than failing on a number. A corpus of other people's Earthfiles needs
// networks, credentials and tools this machine does not have, and a test that
// went red for those would be a test nobody reads.
func TestCorpusTargetsActuallyBuild(t *testing.T) { // not parallel: boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_BUILD") == "" {
		t.Skip("set EARTH_TEST_BUILD=1 to build corpus targets rather than plan them")
	}

	requireSandbox(t)

	guest := buildGuestd(t)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	// Bounded, and the bound is the point. The first version had neither a
	// deadline nor a cap and ran for half an hour against a corpus of hundreds
	// of targets, printing nothing - which is the same defect as a check whose
	// log is overwritten before anyone reads it. A measurement nobody can watch
	// is not a measurement.
	deadline := time.Now().Add(duration(t, "EARTH_TEST_BUILD_TIME", 10*time.Minute))
	limit := count(t, "EARTH_TEST_BUILD_MAX", 12)

	var built, failed, elsewhere, skipped int

	// EARTH_TEST_BUILD_ONLY runs the targets whose name contains it. A sweep
	// takes half an hour, and half an hour is too long to wait to read one
	// error message - which is how a truncated diagnosis survived two rounds of
	// investigation.
	only := os.Getenv("EARTH_TEST_BUILD_ONLY")

	for _, tc := range buildable(t) {
		if only != "" && !strings.Contains(tc.name(), only) {
			continue
		}

		// Every attempt counts toward the limit, including the ones this machine
		// cannot do: they still pull images and boot sandboxes, and a limit that
		// ignored them ran past what it was asked for.
		if built+failed+elsewhere >= limit || time.Now().After(deadline) {
			skipped++

			continue
		}

		// A subtest per target, because Go streams a subtest's output when it
		// finishes and buffers a single test's until the end: with one test the
		// first result would have arrived with the last.
		t.Run(tc.name(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()

			var out bytes.Buffer

			start := time.Now()

			err := cli.Run(ctx, cli.Options{
				Dir: filepath.Dir(tc.file), Target: tc.target, Out: &out,
			})

			took := time.Since(start).Round(time.Millisecond)

			if err != nil {
				// Two kinds of failure, and adding them up hides the one that
				// matters. A build this *machine* cannot do - an image with no
				// manifest for its architecture - is not the engine failing,
				// and counting it as one would make the number stop moving for
				// a reason nobody can act on.
				if cannotHere(err.Error(), out.String()) {
					elsewhere++

					t.Logf("not for this machine, in %s: %s", took, firstLine(err.Error()))

					return
				}

				failed++

				// Logged, not failed. A corpus of other people's Earthfiles
				// needs networks, credentials and tools this machine does not
				// have, and a suite that went red for those is one nobody reads.
				//
				// In full, unlike the other two buckets. The engine's diagnosis
				// lives in the lines *after* the first - what a missing path had
				// beside it, which architecture an image is - and printing only
				// the first line threw all of it away. Five WITH DOCKER failures
				// were investigated twice over before anyone noticed the answer
				// was being truncated on the way to the log. There are single
				// figures of these; there is room for them.
				//
				// And with the step's own output, which the error no longer
				// carries. The same truncation as the paragraph above, arriving
				// by a different route and undoing its fix, which is why both
				// paragraphs are kept: one fault, twice, days apart.
				//
				// E73 stopped a failing step's output being printed twice - once
				// streamed, once repeated in the error - by having the error say
				// "its output is above" whenever the executor had already shown
				// it. That is right for the front end, where "above" is the
				// terminal. Here "above" is `out`, a buffer this harness
				// collects and never prints, so a failure logged a step name, an
				// exit code, and an instruction to look somewhere that does not
				// exist.
				//
				// **A decision verified against one caller and shipped to two.**
				// The engine did stream it to where it was told; the caller
				// holding the output is the one that has to show it.
				t.Logf("did not build in %s:\n%s\n%s", took, indented(err.Error()), indented(lastLines(out.String(), 20)))

				return
			}

			built++

			// With the cache line, because the timing alone answers "did it
			// run" and this test is also the only place that answers "will it
			// be reusable" across real Earthfiles. A build that is green and
			// stores nothing for Κ₂ looks identical to one that does, and the
			// difference is the whole of S5 (E218).
			t.Logf("built in %s\n  %s", took, cacheLine(out.String()))
		})
	}

	t.Logf("%d built, %d did not, %d not for this machine, %d not attempted (limit %d, deadline %s)",
		built, failed, elsewhere, skipped, limit, deadline.Format(time.Kitchen))
}

// duration reads a time from the environment, or takes the default.
func duration(t *testing.T, name string, def time.Duration) time.Duration {
	t.Helper()

	v := os.Getenv(name)
	if v == "" {
		return def
	}

	d, err := time.ParseDuration(v)
	if err != nil {
		t.Fatalf("%s=%q is not a duration: %v", name, v, err)
	}

	return d
}

// count reads a number from the environment, or takes the default.
func count(t *testing.T, name string, def int) int {
	t.Helper()

	v := os.Getenv(name)
	if v == "" {
		return def
	}

	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		t.Fatalf("%s=%q is not a count", name, v)
	}

	return n
}

// buildTarget is one target worth trying.
type buildTarget struct{ file, target string }

func (b buildTarget) name() string {
	return filepath.Base(filepath.Dir(b.file)) + "+" + b.target
}

// buildable picks corpus targets that could plausibly run on this machine.
//
// Self-contained ones only: nothing naming another repository, no LOCALLY (which
// would run commands on the developer's machine from a corpus of other people's
// Earthfiles), and nothing needing a secret. The point is to exercise this
// engine, not to discover that a stranger's build needs credentials.
func buildable(t *testing.T) []buildTarget {
	t.Helper()

	root := corpusRoot(t)

	var out []buildTarget

	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || fi.Name() != testEarthfile {
			return nil //nolint:nilerr // an unreadable corner of the tree is not this test's problem
		}

		src, err := os.ReadFile(p) //nolint:gosec // a file in this repository
		if err != nil {
			return nil //nolint:nilerr // likewise
		}

		text := string(src)
		for _, skip := range []string{"LOCALLY", "--secret", "github.com/", "SAVE IMAGE --push"} {
			if strings.Contains(text, skip) {
				return nil
			}
		}

		for _, target := range targetsIn(text) {
			out = append(out, buildTarget{file: p, target: target})
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// indented is a whole diagnosis, set in far enough to read as one entry.
func indented(s string) string {
	var out strings.Builder

	for line := range strings.SplitSeq(strings.TrimRight(s, "\n"), "\n") {
		out.WriteString("      ")
		out.WriteString(line)
		out.WriteString("\n")
	}

	return strings.TrimRight(out.String(), "\n")
}

// firstLine is the part of a diagnosis that fits on one, which is what a
// summary of forty builds can show.
func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}

	return s
}

// targetsIn lists the targets an Earthfile defines.
func targetsIn(src string) []string {
	var out []string

	for line := range strings.SplitSeq(src, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}

		name, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			continue
		}

		// Not a target: VERSION and PROJECT are file-level, and a base recipe
		// has no name.
		if strings.ToUpper(name) == name {
			continue
		}

		out = append(out, name)
	}

	return out
}

// cannotHere reports whether a failure is this machine's rather than this
// engine's.
//
// An image that provides no manifest for the sandbox's architecture cannot be
// run here by anything, and counting it as an engine failure would make the
// number stop moving for a reason nobody can act on. Matched on the engine's
// own wording, which is why that wording is a single sentence in one place.
func cannotHere(msg, output string) bool {
	for _, s := range []string{
		"cannot be executed here",
		"it is a single-manifest image",
		"binary for another architecture",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}

	// An image holding two paths that differ only in case, unpacked onto a
	// filesystem that cannot hold both. `python:3` ships `PAM.7.gz` beside
	// `pam.7.gz`; `earthbuild/dind` ships `libip6t_HL.so` beside its lower-case
	// twin. Neither is unpackable here by anything, and the engine says so
	// before it tries - naming both paths, the layer and the reason. This is the
	// commonest failure in the corpus by a distance: 17 of 26 in the first full
	// sweep, and all of them a property of the disk.
	if strings.Contains(msg, "differ only in case") {
		return true
	}

	// A registry that answered badly. Not this engine, and not this machine
	// either - a bad minute at a registry, which is E15's territory rather than
	// a defect anybody can act on from here.
	//
	// Anchored on the request rather than the number: a step is perfectly
	// entitled to print "502" itself, and a build that failed because *its own*
	// server misbehaved is a build that failed. Both halves have to be in the
	// engine's own message about a fetch.
	if strings.Contains(msg, "registry-1.docker.io") || strings.Contains(msg, "://") {
		for _, code := range []string{"502", "503", "504", "429"} {
			if strings.Contains(msg, "returned "+code) {
				return true
			}
		}
	}

	// A step that asked the filesystem about its own case behaviour, on a store
	// that cannot answer consistently.
	//
	// Both halves are required. ESTALE alone is a symptom this engine could
	// perfectly well have caused, and laundering every one of them would hide
	// the failures this count exists to show. The note is the engine's own
	// finding about the disk it was handed, so the pair means "this Mac", not
	// "this engine" - `examples/next-js` fails this way on a stock Mac and
	// builds end to end when the store is case-sensitive (E25).
	if strings.Contains(output, "is on a case-insensitive filesystem") &&
		strings.Contains(output, "stale file handle") {
		return true
	}

	return false
}

// corpusRoot is a copy of the corpus, in a directory that is thrown away.
//
// Built in a copy because `SAVE ARTIFACT ... AS LOCAL` writes where the
// Earthfile says, and for a corpus of tutorials that is next to their sources.
// A full sweep left 35 files in the repository - jars, bundled javascript,
// compiled binaries - every one untracked and every one indistinguishable from
// work once staged.
//
// The whole tree is copied rather than each Earthfile's own directory: an
// example may reach a sibling, and a corpus that half-works is worse than one
// that does not run.
func corpusRoot(t *testing.T) string {
	t.Helper()

	// Copied from the *repository root*, not from the subtree being walked.
	//
	// Earthfiles in a monorepo reach upwards - `FROM ../..+base` is ordinary,
	// and every Earthfile under tests/ does it - so a copy of the subtree alone
	// breaks all of them with "no Earthfile for this reference". The reference
	// has to be inside the copy or it is not the same corpus.
	repo := filepath.Join("..", "..")

	sub := os.Getenv("EARTH_TEST_CORPUS")
	if sub == "" {
		sub = "examples"
	}

	dst := filepath.Join(t.TempDir(), "corpus")

	err := copyCorpus(repo, dst)
	if err != nil {
		t.Fatalf("copy the corpus: %v", err)
	}

	return filepath.Join(dst, sub)
}

// skipInCorpus are directories a build makes rather than reads.
//
// The reason is arithmetic: `examples/` is 958 MB on this machine and nearly
// all of it is node_modules and .next, left behind by builds. Copying that per
// sweep costs more than the sweep. None of it is input - a build that needs
// node_modules installs them - and .git is neither input nor small.
var skipInCorpus = map[string]bool{
	".git":         true,
	"node_modules": true,
	".next":        true,
	"vendor":       true,
}

// bigTreePrefix names `engine/store`'s generated fixture.
//
// A hundred thousand files written into gitignored `testdata/` on demand, so it
// is what `skipInCorpus` is about - a build makes it rather than reads it - and
// it is eighty megabytes per sweep. It is also *live*: the fixture is staged as
// `bigtree-20000.building-<pid>` and renamed, so a walk that copies it races the
// package that is writing it (E616). Not copying it is the fix and the saving at
// once.
const bigTreePrefix = "bigtree-"

// skipCorpusDir reports a directory a build makes rather than reads.
func skipCorpusDir(name string) bool {
	return skipInCorpus[name] || strings.HasPrefix(name, bigTreePrefix)
}

// vanished reports a file that stopped existing between being listed and being
// looked at.
//
// Not an error worth failing a walk for: `engine/store` generates and renames a
// fixture inside the tree while other packages walk it, so a path can be
// enumerated and gone a moment later. Everything else still fails - a corpus
// this engine cannot *read* is a corpus it must not silently copy half of.
func vanished(err error) bool {
	return err != nil && errors.Is(err, fs.ErrNotExist)
}

// copyCorpus copies a tree, leaving out what a build would only make again.
//
// Not os.CopyFS, which cannot decline a directory: it copied 1.3 GB and
// seventeen seconds per call, which is a tax on every corpus run to carry
// things no Earthfile reads.
func copyCorpus(src, dst string) error {
	root, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("resolve the corpus root: %w", err)
	}

	return filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if vanished(err) {
			return nil
		}

		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}

		if fi.IsDir() {
			if skipCorpusDir(fi.Name()) {
				return filepath.SkipDir
			}

			return os.MkdirAll(filepath.Join(dst, rel), 0o750) //nolint:wrapcheck // the caller says which corpus
		}

		// Symlinks are recreated rather than followed: a link into a directory
		// that was skipped would otherwise copy it back in.
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}

			return os.Symlink(target, filepath.Join(dst, rel)) //nolint:wrapcheck // as above
		}

		if !fi.Mode().IsRegular() {
			return nil
		}

		b, err := os.ReadFile(p) //nolint:gosec // a path from this repository
		if err != nil {
			return err
		}

		return os.WriteFile(filepath.Join(dst, rel), b, fi.Mode().Perm()) //nolint:wrapcheck // as above
	})
}

// lastLines is the tail of a build's output, which is where a failure is.
//
// Bounded because a whole build's streamed output is thousands of lines and the
// only part that explains an exit code is the part just before it. Twenty is
// enough for a compiler's error or a package manager's, and short enough that a
// dozen failures still fit in one log somebody will read.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}

	return strings.Join(lines[len(lines)-n:], "\n")
}

// cacheLine pulls the cache summary out of a build's output.
//
// The summary is one row of a table the engine prints; this finds it rather than
// reproducing the format, so a change to the row does not need a change here.
func cacheLine(log string) string {
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "cache ") {
			return strings.Join(strings.Fields(l), " ")
		}
	}

	return "(no cache summary)"
}
