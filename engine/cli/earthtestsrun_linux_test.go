//go:build linux && integration

package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/internal/corpus"
)

// How many of the `tests/` tree this engine can actually build.
//
// The test plan has named this gate since M1 - "a2. Ratcheted test-count gate" -
// and what existed was a *planning* sweep (E410). Planning says the graph is
// right; it cannot say the bytes are. The first bounded run of this proved the
// difference immediately, finding three bugs in an afternoon that a week of
// planning at 84% had not: an ENV value set literally (E422), the builtin
// arguments missing (E423), and `ARG --global` reaching no function (E425).
//
// **Every file, one target in each.** The file's `all` or `test` where it
// declares one, because several declare a helper first and the target that
// drives it second (E445).
//
// One target per file is the coverage this still does not have, and it is stated
// here rather than hidden: a file's later targets are never built. That bound is
// about what a target *means* - the tree's own convention is that one entry
// target drives the rest - rather than about what the gate can afford, which is
// what the removal of every other bound was about (E453).
func TestHowManyEarthTestsBuild(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	guest := buildGuestd(t)
	cache := storeDir(t)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, cache)

	// The tree's location, not a path relative to this file.
	//
	// `go test` runs with the package directory as the working directory and a
	// compiled binary runs with whatever the caller had - so `../../tests`
	// resolved to the repository under one and to nothing under the other. The
	// test passed locally and *skipped* in the container, reporting a count of
	// zero targets found rather than a failure, which is the shape of every
	// silent-skip this project has recorded (E429).
	//
	// `EARTH_CORPUS_DIR` is the same setting the planning sweep uses for the
	// same reason, and the gate sets it.
	root := os.Getenv("EARTH_CORPUS_DIR")
	if root == "" {
		root = filepath.Join("..", "..")
	}

	found, err := filepath.Glob(filepath.Join(root, "tests", "*.earth"))
	if err != nil {
		t.Fatal(err)
	}

	sort.Strings(found)

	// What the tree says it wants built, rather than what this gate guessed.
	//
	// `tests/Earthfile` drives the corpus with its own `RUN_EARTH` function,
	// naming the file, the target and the arguments each needs - and saying
	// which are meant to fail. Reading it turns a guess into a reading (E454,
	// E455).
	var work []corpus.Invocation

	// Read as a sequence rather than line by line: an invocation naming no file
	// reuses the one the invocation before it copied, and a target header
	// resets that (E470).
	for _, in := range corpus.Invocations(readCorpusFile(t, "tests/Earthfile")) {
		if in.Exec != "" || (in.File == "" && in.Target == "") {
			// A script rather than a build, or a line that named nothing this
			// gate can act on.
			continue
		}

		work = append(work, in)
	}

	if len(work) < 200 {
		t.Skipf("read %d invocations from tests/Earthfile, and it has hundreds",
			len(work))
	}

	// A skip that names where it looked. The first version said only how many it
	// found, which is the same message whether the tree is missing or the path
	// is wrong - and it was the path.
	if len(found) < 50 {
		t.Skipf("found %d .earth files under %s, fewer than a whole checkout has"+
			"\n  set EARTH_CORPUS_DIR to the repository root", len(found), root)
	}

	// There is no bound on how much of the tree is looked at, and there was one
	// until an hour ago: twelve files, then forty, then eighty. Every one of
	// those was set by how long the gate took rather than by anything about the
	// engine - **a sample was never the point, it was the price** (E453).
	//
	// The two bounds that remain are about hanging rather than coverage: a
	// per-target deadline (E442's release wait sat for thirteen minutes under
	// one that reached nobody) and the test's own timeout.

	// Per target, because one target that hangs must not consume the whole
	// budget - and named, because it is quoted in the report when a target
	// spends it (E447).
	const perTarget = 60 * time.Second

	// How many targets are built at once.
	//
	// The bound that mattered was wall-clock, not the engine: one target per
	// file, serially, is an hour for the tree and so the gate looked at a
	// fraction of it. Four, because each build starts a sandbox and pulls
	// images, and a machine that swaps measures its own memory pressure (E453).
	const workers = 4

	// copyCostMB is roughly what one worker's copy of `tests/` takes at the
	// moment it is made, so a machine that runs out can be told how much it was
	// short of rather than only that it ran out. Approximate on purpose: the
	// number exists to size an error message, and a tree *grows* while it is
	// used - several targets write into it, `remote-cache/test2/node_modules`
	// most of all.
	const copyCostMB = 40

	// One tree per worker, so a target may refer to its neighbours and to the
	// repository's own root Earthfile - and so that several can be built at
	// once.
	//
	// The file under test is written as `tests/Earthfile`, which is one name: a
	// serial gate can share a tree and a concurrent one cannot. Per worker
	// rather than per target, because the tree is 38 megabytes and the file
	// under test is the only thing that changes (E453).
	//
	// `tests/` and that one file, not the whole checkout: the rest is source,
	// build output and a git directory, and copying it would cost hundreds of
	// megabytes for nothing an Earthfile can see.
	trees := make([]string, workers)

	for i := range trees {
		trees[i] = t.TempDir()

		if err := os.CopyFS(filepath.Join(trees[i], "tests"),
			os.DirFS(filepath.Join(root, "tests"))); err != nil {
			// Fatal rather than Skip. A machine that cannot host the gate is a
			// failure to report, and a skipped gate prints `ok` for the package
			// - *a skip and a pass are the same word*, which is the lesson E466
			// learned about a socket path and which this file was still getting
			// wrong about a full disk (E472).
			t.Fatalf("cannot copy the corpus tree, so nothing would be built"+
				"\n  %v\n  the gate needs about %d MB of scratch: %d worker"+
				" trees of the corpus, and each is a full copy",
				err, workers*copyCostMB, workers)
		}

		if src, err := os.ReadFile(filepath.Join(root, "Earthfile")); err == nil {
			err := os.WriteFile(filepath.Join(trees[i], "Earthfile"), src, 0o600)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	var (
		built    int
		skipped  []string
		timedOut []string
		why      = map[string]int{}
		names    = map[string][]string{}
	)

	// Built four at a time, and tallied in one place afterwards.
	//
	// Named as each is attempted, because the first run of this gate timed out
	// after 23 minutes and the failure said only that: a panic, a goroutine dump
	// and no indication of which target had it (E428). Concurrently now, which
	// is what makes the whole tree affordable rather than a sample of it (E453).
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		done []outcome
	)

	queue := make(chan corpus.Invocation)

	go func() {
		defer close(queue)

		for _, in := range work {
			queue <- in
		}
	}()

	wg.Add(workers)

	for w := range workers {
		go func(tree string) {
			defer wg.Done()

			for in := range queue {
				// Named before it is attempted. The first run of this gate timed
				// out after 23 minutes and said only that - a panic, a goroutine
				// dump, and no indication of which target had it (E428) - and
				// the line was lost again in the rewrite that started reading
				// the tree's invocations, which is how a diagnostic dies.
				t.Logf("attempting %s+%s", in.File, in.Target)

				got := attemptOne(t, tree, in, root, perTarget)

				mu.Lock()
				done = append(done, got)
				mu.Unlock()
			}
		}(trees[w])
	}

	wg.Wait()

	// Sorted, because four workers finish in whatever order the machine gives
	// them and a report that changes between runs of the same tree is one
	// nobody can diff (E442's list, under E453's concurrency).
	slices.SortFunc(done, func(a, b outcome) int { return strings.Compare(a.file, b.file) })

	var (
		unpassable []string
		wrongWay   []string
		onPurpose  []string
	)

	for _, got := range done {
		where := got.file
		if got.target != "" {
			where += "+" + got.target
		}

		switch {
		case got.unpassable != "":
			unpassable = append(unpassable, where+" ("+got.unpassable+")")

		case got.noTarget:
			skipped = append(skipped, where)

		case got.timedOut:
			timedOut = append(timedOut, where)

		// Asked before the tree's own expectation, because a deliberate
		// refusal is the answer whichever way the tree wanted it to go: a
		// target that needs a construct this engine refuses on purpose is
		// neither built nor pending.
		case got.onPurpose:
			onPurpose = append(onPurpose, where+" ("+got.reason+")")

		// The tree said this one is meant to be refused, and it was.
		//
		// Counting a declared refusal as a failure is how a file whose whole
		// purpose is to be refused read as an engine defect for six increments
		// (E455).
		case got.expected && !got.built:
			built++

			names["built"] = append(names["built"], where+" (refused, as declared)")

		case got.expected && got.built:
			// Worse than a failure: the tree says this must not build and it
			// did. Kept apart from the rest because it is the only outcome here
			// that means the engine did something it was told not to.
			wrongWay = append(wrongWay, where)

		case got.built:
			built++

			names["built"] = append(names["built"], where)

		default:
			why[got.reason]++

			names[got.reason] = append(names[got.reason], where)
		}
	}

	if len(unpassable) > 0 {
		t.Logf("%d invocation(s) were not attempted, because this gate cannot"+
			" pass an option the tree gives them: %s"+
			"\n  an invocation driven without an option it was given is a"+
			" different invocation", len(unpassable), strings.Join(unpassable, " "))
	}

	if len(wrongWay) > 0 {
		t.Errorf("%d target(s) built that the tree says must fail: %s"+
			"\n  a build that succeeds where the Earthfile says it must not is"+
			" the engine doing what it was told not to",
			len(wrongWay), strings.Join(wrongWay, " "))
	}

	if len(onPurpose) > 0 {
		t.Logf("%d target(s) need a construct this engine refuses on purpose,"+
			" and are out of the denominator: %s"+
			"\n  each refusal names itself and says where the reason is"+
			" written; they are divergences rather than gaps",
			len(onPurpose), strings.Join(onPurpose, " "))
	}

	if len(timedOut) > 0 {
		t.Logf("%d target(s) ran out of their %s and are counted as unbuilt: %s"+
			"\n  a cold image pull shares that budget, so this is the gate's"+
			" clock rather than the engine's speed",
			len(timedOut), perTarget, strings.Join(timedOut, " "))
	}

	// The denominator is what the gate could have built, not what it looked at.
	//
	// Three of the twelve files in the first slice are base recipes with no
	// target in them at all - `tests/arg-set.earth` is five lines and declares
	// none - so counting them in the denominator understates the engine by a
	// quarter. The planning sweep makes the same distinction and says why
	// (E413); this is the same rule for the same reason (E440).
	// What was attempted, minus what could not be. The denominator is the tree's
	// own invocations now rather than the files on disk (E455).
	judged := len(work) - len(skipped) - len(unpassable) - len(onPurpose)

	if len(skipped) > 0 {
		t.Logf("%d file(s) declare no target at all, so they are not in the"+
			" denominator: %s", len(skipped), strings.Join(skipped, " "))
	}

	// Named, not just counted.
	//
	// Two runs of this gate gave 19 and 18, and a count alone cannot say which
	// target moved - so a ratchet on it is a number that flakes with no way to
	// diagnose the flake. The list makes two runs diffable (E442).
	t.Logf("%d of %d invocations answer as the tree says, from %d files"+
		"\n  built: %s",
		built, judged, len(found), strings.Join(names["built"], " "))

	// Ordered by how many targets each reason accounts for, because the work
	// list is what this gate is for now: the biggest group is the next thing
	// worth fixing, and the name beside it is where to start.
	reasons := make([]string, 0, len(why))
	for reason := range why {
		reasons = append(reasons, reason)
	}

	sort.Slice(reasons, func(i, j int) bool {
		if why[reasons[i]] != why[reasons[j]] {
			return why[reasons[i]] > why[reasons[j]]
		}

		return reasons[i] < reasons[j]
	})

	for _, reason := range reasons {
		t.Logf("  x%d %s\n     %s", why[reason], reason,
			strings.Join(names[reason], " "))
	}

	ratchetRun(t, built)
}

// entryTarget is the target a corpus file is meant to be built by.
//
// **Not simply the first one.** `tests/build-arg-dynamic-with-empty-base.earth`
// declares `subtest` first - a helper that takes an argument and asserts what it
// holds - and `test` second, which is the one that supplies it. Built as the
// gate had it, the helper ran with an empty argument and failed, and the report
// said the engine could not build the file (E445).
//
// `all` then `test` then the first, which is the tree's own convention: 18 of
// the first 40 files declare one or the other, and `tests/Earthfile` drives them
// by those names.
func entryTarget(src string) string {
	declared := map[string]bool{}
	for _, t := range targets(src) {
		declared[t] = true
	}

	for _, preferred := range []string{"all", "test"} {
		if declared[preferred] {
			return preferred
		}
	}

	return firstTarget(src)
}

// targets are every target a file declares, in order.
func targets(src string) []string {
	var out []string

	for _, line := range strings.Split(src, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}

		name, _, ok := strings.Cut(line, ":")
		if ok && name != "" && !strings.Contains(name, " ") && !strings.HasPrefix(name, "VERSION") {
			out = append(out, name)
		}
	}

	return out
}

// firstTarget is the first target a file declares.
func firstTarget(src string) string {
	for _, line := range strings.Split(src, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}

		name, _, ok := strings.Cut(line, ":")
		if ok && name != "" && !strings.Contains(name, " ") && !strings.HasPrefix(name, "VERSION") {
			return name
		}
	}

	return ""
}

// outcome is what one corpus file's attempt came to.
type outcome struct {
	file string
	// target names which target of the file was built, for the report.
	target string
	// expected is what the tree said should happen: true when the invocation
	// declares `--should_fail` (E455).
	expected bool
	// unpassable names an option this gate could not give the build, empty when
	// it gave all of them.
	unpassable string
	// built is true when the target built.
	built bool
	// noTarget is true when the file declares none this gate could find.
	noTarget bool
	// timedOut is true when the attempt spent its whole budget.
	timedOut bool
	// onPurpose is true when the engine refused a construct it refuses
	// deliberately, with the reason written where it is refused.
	//
	// Separate from a failure because it is a different fact: `SAVE ARTIFACT
	// --force` writes outside the project and this engine does not, so a corpus
	// target that needs it can never build here and is not waiting for anybody.
	// Counted with the failures it reads as a defect nobody has fixed, and *a
	// number that cannot reach zero is a number nobody reads* (E473).
	onPurpose bool
	// reason is the first line of the failure, empty for the other outcomes.
	reason string
}

// attemptOne builds one corpus file's entry target and reports what happened.
//
// It returns rather than records: four builds run at once, and a function that
// wrote into the tally would need a lock around work that is nine-tenths
// waiting. The caller collects, in one place, serially (E453).
func attemptOne(t *testing.T, tree string, in corpus.Invocation, root string, perTarget time.Duration) outcome {
	t.Helper()

	// The file the tree named, or its own Earthfile when it named none.
	name := in.File
	if name == "" {
		name = "Earthfile"
	}

	got := outcome{file: name, target: in.Target, expected: in.ShouldFail}

	opts, why := passable(in)
	if why != "" {
		got.unpassable = why

		return got
	}

	src, err := os.ReadFile(filepath.Join(root, "tests", name))
	if err != nil {
		got.noTarget = true

		return got
	}

	// A reading command answers from the file and builds nothing, so it is
	// answered here and the rest of this - the entry target, the timeout, the
	// sandbox, the expectation - is about builds. Before the target is worked
	// out rather than after: these invocations name none, so a gate that looked
	// for one first would report them as files declaring no target (E474).
	//
	// What it checks is that the command answers, not what it printed: the
	// tree's own assertion is a pcregrep over the output, and `tests/Earthfile`
	// says where the detailed coverage lives. Here that is
	// `TestDocPrintsDocumentedTargets` and its neighbours, which hold the shape
	// against the same corpus files.
	if verb := verbOf(in); verb != "" {
		dir := filepath.Join(tree, "tests")

		err := os.WriteFile(filepath.Join(dir, "Earthfile"), src, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		opts.Dir, opts.Out = dir, &bytes.Buffer{}

		read := cli.List
		if verb == "doc" {
			read = cli.Doc
		}

		err := read(opts)
		if err != nil {
			got.reason = firstLine(err.Error())

			return got
		}

		got.built = true

		return got
	}

	target := in.Target
	if target == "" {
		target = entryTarget(string(src))
	}

	if target == "" {
		// Counted, not dropped. A file this gate cannot find a target in is
		// attempted-and-not-attempted: it is in the denominator and in neither
		// the successes nor the failures, so the numbers stop adding up and
		// nobody notices which files went missing (E440).
		got.noTarget = true

		return got
	}

	got.target = target

	// Written into this worker's copy of the tree, not into an empty directory.
	//
	// A corpus file may refer to its neighbours - `FROM ../+base`,
	// `IMPORT ./a/really/deep/subdir` - which is ordinary and which nothing
	// resolves when the file is alone in a temporary directory (E440).
	dir := filepath.Join(tree, "tests")

	err = os.WriteFile(filepath.Join(dir, "Earthfile"), src, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Per target, because one target that hangs must not consume the whole
	// budget and leave the rest unattempted - which is indistinguishable from
	// them failing.
	ctx, done := context.WithTimeout(context.Background(), perTarget)
	defer done()

	var out bytes.Buffer

	opts.Dir, opts.Target, opts.Out, opts.Platform = dir, target, &out, testPlatform()

	err = cli.Run(ctx, opts)
	if err == nil {
		got.built = true

		return got
	}

	// The first line of the error, because these are rustc-shaped diagnostics
	// whose first line is the claim and whose rest is the advice: the claim is
	// what groups (E438).
	reason := firstLine(err.Error())

	// A target that ran out of time measures the budget, not the engine: a cold
	// image pull shares that deadline (E447).
	if errors.Is(err, interp.ErrOnPurpose) {
		got.onPurpose, got.reason = true, reason

		return got
	}

	if strings.Contains(reason, "context deadline exceeded") {
		got.timedOut = true

		return got
	}

	got.reason = reason

	return got
}
