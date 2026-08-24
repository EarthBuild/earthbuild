package interp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// plannedTarget is one target of one corpus Earthfile that planned.
type plannedTarget struct {
	target string
	plan   *interp.Plan
}

// plannedFile is a corpus Earthfile and every target in it that planned.
type plannedFile struct {
	file  string
	plans []plannedTarget
}

var (
	corpusPlansOnce sync.Once
	corpusPlansAll  []plannedFile
	corpusPlansErr  error
	corpusPlansRoot string
)

// corpusPlans is every target in the corpus that plans, planned once.
//
// **Six sweeps were building the same graphs.** They assert different things -
// that a graph schedules, that every artifact is produced, that no consumed
// syntax survives - about identical input, and each of them planned all of it
// again: around 160 of this package's 300 seconds was one planning pass done
// six times over. Most of that is not parsing. A CPU profile puts two thirds of
// the package in `layer.contentDigest`, because planning a COPY digests the
// build context it names, and the corpus is this repository.
//
// **Shared because sharing is safe here, which is a claim and not a hope.**
// `Build` is a function of its arguments; the corpus is a copy nothing writes
// to; and `Scheduler.Run` keeps its per-build state on the scheduler rather
// than on the graph it is handed, so a sweep that schedules a plan does not
// leave anything behind in it. A sweep that *mutated* a plan would be editing
// another sweep's fixture, which is the one way this can go wrong - and the
// reason it says so here rather than leaving the next reader to find out.
//
// Refusals are not carried, because all six skip them. What a refusal ought to
// say is TestCorpusIsAcceptedOrRefusedActionably's subject, and that one still
// plans the corpus itself.
func corpusPlans(t *testing.T) []plannedFile {
	t.Helper()

	corpusPlansOnce.Do(buildCorpusPlans)

	if corpusPlansErr != nil {
		t.Fatal(corpusPlansErr)
	}

	return corpusPlansAll
}

// buildCorpusPlans does the one pass, with no test to report to.
//
// Its own copy of the tree rather than `corpus`'s, because that one is under a
// `t.TempDir` belonging to whichever test asked first - which is removed when
// that test ends, while the other five are still reading paths out of it.
func buildCorpusPlans() {
	root := corpusTree()

	for _, f := range earthfilesUnder(root) {
		src, readErr := os.ReadFile(f)
		if readErr != nil {
			corpusPlansErr = readErr

			return
		}

		entry := plannedFile{file: f}

		for _, target := range targetsIn(string(src)) {
			p, buildErr := interp.Build(string(src), target, interp.WithContext(filepath.Dir(f)))
			if buildErr != nil {
				continue
			}

			entry.plans = append(entry.plans, plannedTarget{target: target, plan: p})
		}

		if len(entry.plans) > 0 {
			corpusPlansAll = append(corpusPlansAll, entry)
		}
	}
}

// corpusTree is an immutable copy of the tree to sweep.
//
// **Always a copy, and that is the load-bearing word.** A target whose context
// is the whole repository - `COPY . .`, which `+markdown-spellcheck` does -
// hashes every file in it, so anything editing the tree while a sweep reads it
// changes the answer legitimately and the sweep reports the planner as
// non-deterministic. Three investigations went into that before anybody noticed
// the editor was open (E70). One copy for the binary is 0.4 seconds and removes
// the whole class.
//
// Where the files come from has three answers, the same three `corpus` gives
// and in the same order. An explicit `EARTH_CORPUS_DIR` wins, because the binary
// may be running somewhere that is not the package directory - a container with
// the tree mounted. Failing that, what git says is tracked, so the sweep does
// not depend on what a developer has lying about (E562). And where git will not
// answer - an exported tree, or `+code/earthly`, which carries the source and no
// `.git` - the working directory is walked instead, because a corpus test that
// refuses to run finds nothing (I11).
//
// The first version had only the middle answer and CI exited 128 on every corpus
// sweep at once.
func corpusTree() string {
	root, err := os.MkdirTemp("", "corpus") // outlives any one test; see above
	if err != nil {
		return "../.."
	}

	from := os.Getenv("EARTH_CORPUS_DIR")
	if from == "" {
		if copyTrackedTo(root) == nil {
			corpusPlansRoot = root

			return root
		}

		from = "../.."
	}

	err = copyTreeTo(from, root)
	if err != nil {
		_ = os.RemoveAll(root)

		return from
	}

	corpusPlansRoot = root

	return root
}

// copyTreeTo copies a tree, leaving out what no build reads.
//
// The same exclusions the corpus itself uses plus what a build leaves behind.
// `examples` alone is most of a gigabyte of node_modules on a machine that has
// built them, and none of it is input.
func copyTreeTo(src, dst string) error {
	skip := map[string]bool{
		".git": true, "node_modules": true, ".next": true, "build": true, "vendor": true,
	}

	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not this fixture's problem
		}

		if fi.IsDir() {
			if skip[fi.Name()] {
				return filepath.SkipDir
			}

			return nil
		}

		if !fi.Mode().IsRegular() {
			return nil
		}

		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return nil //nolint:nilerr // ditto
		}

		b, readErr := os.ReadFile(filepath.Clean(p))
		if readErr != nil {
			return nil //nolint:nilerr // ditto
		}

		at := filepath.Join(dst, rel)

		mkErr := os.MkdirAll(filepath.Dir(at), 0o750)
		if mkErr != nil {
			return mkErr
		}

		return os.WriteFile(at, b, 0o600)
	})
}

// copyTrackedTo writes this repository's tracked files under dst.
func copyTrackedTo(dst string) error {
	out, err := exec.CommandContext(
		context.Background(), "git", "-C", "../..", "ls-files", "-z").Output()
	if err != nil {
		return err
	}

	for rel := range strings.SplitSeq(string(out), "\x00") {
		if rel == "" {
			continue
		}

		b, readErr := os.ReadFile(filepath.Clean(filepath.Join("../..", rel)))
		if readErr != nil {
			// Tracked but not present is a deleted-and-unstaged file, which a
			// working tree is allowed to be.
			continue
		}

		at := filepath.Join(dst, rel)

		err = os.MkdirAll(filepath.Dir(at), 0o750)
		if err != nil {
			return err
		}

		err = os.WriteFile(at, b, 0o600)
		if err != nil {
			return err
		}
	}

	return nil
}

// earthfilesUnder finds the corpus within a copied tree.
func earthfilesUnder(root string) []string {
	var found []string

	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not this fixture's problem
		}

		if fi.IsDir() && (fi.Name() == "node_modules" || fi.Name() == ".git") {
			return filepath.SkipDir
		}

		if !fi.IsDir() && fi.Name() == testEarthfile {
			found = append(found, p)
		}

		return nil
	})

	sort.Strings(found)

	return found
}

// TestMain removes the shared corpus copy, which no test owns.
func TestMain(m *testing.M) {
	code := m.Run()

	if corpusPlansRoot != "" {
		_ = os.RemoveAll(corpusPlansRoot)
	}

	os.Exit(code)
}
