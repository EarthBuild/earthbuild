package interp_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	corpuslib "github.com/EarthBuild/earthbuild/internal/corpus"
)

// The `tests/` tree is a corpus nothing was sweeping.
//
// The corpus test walks the repository for files *named* `Earthfile` - and 116
// Earthfiles in `tests/` are named `*.earth` instead, so none of them had ever
// been handed to this engine. They are the old engine's test cases: written
// against the language rather than against an implementation, by people with no
// knowledge of this one, which is the corpus property that makes the sweep worth
// anything.
//
// The test plan has described this gate since M1 as "a2. Ratcheted test-count
// gate" and it did not exist. *A mechanism named in a plan and never built reads,
// from the plan, exactly like one that is running.*
//
// Separate from the whole-corpus count rather than folded into it, on E389's
// argument: a population of 116 files inside a total of 489 targets moves the
// total by too little to be seen.
// harnessCondition reports whether a refusal is this sweep's doing.
//
// A `tests/*.earth` file is run by a harness that copies it somewhere as
// `Earthfile`, puts the context files it names beside it, and builds the targets
// it references. This sweep hands the file to the interpreter where it lies and
// does none of that, so these three refusals say nothing about the engine.
//
// **A judgement, written where it can be argued with.** E411 made the same
// discount in a paragraph - "roughly 63%" - which nobody can test and nobody can
// disagree with precisely. Here it is three strings and a test that pins which
// side each falls on, including the ones that must *not* be discounted.
//
// Matched on the message rather than on a sentinel because these come from
// several places in the interpreter and share no error type; a sentinel for each
// would be a change to the engine made to suit a test of the engine.
func harnessCondition(what string) bool {
	for _, ours := range []string{
		"missing context file",
		"unknown target",
		"no Earthfile for this reference",
	} {
		if strings.Contains(what, ours) {
			return true
		}
	}

	return false
}

func TestTheEarthTestsSweep(t *testing.T) {
	t.Parallel()

	root := os.Getenv("EARTH_CORPUS_DIR")
	if root == "" {
		root = trackedCopy(t)
	}

	found, err := filepath.Glob(filepath.Join(root, "tests", "*.earth"))
	if err != nil {
		t.Fatal(err)
	}

	sort.Strings(found)

	if len(found) < 50 {
		t.Skipf("found %d .earth files, fewer than a whole checkout has", len(found))
	}

	// The tree's own account of which targets are meant to be refused.
	//
	// Six of `save-artifact-dont-overwrite.earth`'s targets exist to be refused,
	// and this sweep had been counting the engine refusing them as work left to
	// do. **A refusal counted as a gap is a number that cannot reach zero** -
	// the run gate learned this at E455 and read the same flag to fix it; this
	// reads it through the same code rather than a second copy (E477).
	meantToFail := corpuslib.MeantToFail(readTree(t, root))

	// The same fetcher the corpus sweep uses, which resolves a remote reference
	// from a checkout on this machine and declines to reach the network.
	//
	// Without it every remote reference refused, and those refusals were being
	// counted as engine gaps - 19 of them, on a list read to decide what to
	// build next. The engine has the mechanism; this sweep was withholding it
	// (E417).
	fetch := localRemotes(t)

	var (
		planned int
		// plans names every target that planned, so two machines disagreeing
		// about the count can be diffed rather than argued about. See
		// ratchetSlice.
		plans []string
		total int
		// sweeps counts refusals this sweep caused, which are not the engine's
		// and must not be counted as work left.
		sweeps int
		// Three ways a refusal is not work, counted apart.
		//
		// One number covering all three read as "invalid Earthfiles" and was
		// none of them by itself: **a number that names three things is a
		// number nobody can act on** (E477).
		//
		// invalid: the file is wrong and this engine says so.
		invalid int
		// declaredFailing: the tree drives this target with `--should_fail`, so
		// the refusal is the assertion passing.
		declaredFailing int
		// onPurpose: a construct this engine refuses deliberately, with the
		// reason written where it is refused.
		onPurpose int
		refused   = map[string]int{}
	)

	for _, f := range found {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}

		func() {
			// A panic is an outcome under test as much as an error is.
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked: %v", f, r)
				}
			}()

			for _, target := range targetsIn(string(src)) {
				total++

				opts := []interp.Option{interp.WithContext(filepath.Dir(f))}
				if fetch != nil {
					opts = append(opts, interp.WithRemotes(fetch))
				}

				_, err := interp.Build(string(src), target, opts...)
				if err == nil {
					planned++
					// Relative to the corpus, not absolute: the sweep runs in
					// a temporary directory whose name is different every run,
					// and a list carrying it differs from itself.
					plans = append(plans, relTo(root, f)+"+"+target)

					continue
				}

				// The corpus sweep's own classifier, so the two populations are
				// described in one vocabulary. A second way of naming the same
				// refusals would make the two counts uncomparable, which is most
				// of the value of having both.
				// The tree's own path stripped out. `classify` returns the
				// engine's message, which names the file - and the file is under
				// a fresh temporary directory every run, so counting the message
				// verbatim makes every refusal unique and the tally useless. The
				// count is of *constructs*, not of locations.
				construct, _ := classify(err)
				what := strings.ReplaceAll(construct, root, "")

				// A capability this sweep withheld is not a gap in the engine.
				// `ErrNotProvided` is exactly that distinction, drawn by the
				// interpreter and used by the corpus sweep for the same reason.
				if harnessCondition(construct) || errors.Is(err, interp.ErrNotProvided) {
					sweeps++

					continue
				}

				// An Earthfile that is invalid on purpose is not work either,
				// and neither is a construct this engine refuses deliberately.
				//
				// The third category, and the last one the run gate learned
				// before this sweep did: `SAVE ARTIFACT --force` writes outside
				// the project and this engine does not, so a target needing it
				// is a divergence with a reason written at the refusal rather
				// than a gap waiting for somebody (E473, E477).
				switch {
				// The tree's own statement first, because it is about *this
				// target* rather than about the shape of the refusal: a file
				// driven with `--should_fail` is one whose refusal is the
				// assertion passing, and reporting it as a broken Earthfile is
				// true but useless.
				case meantToFail[filepath.Base(f)+"+"+target]:
					declaredFailing++

					continue

				// Then the engine's own statement, not this sweep's reading of
				// the text.
				//
				// Three sentinels divide a refusal: the caller's to fix, a
				// decision nobody should fix, and work. Anything carrying none
				// of them says the *Earthfile* is wrong, which is the corpus
				// sweep's rule already - and having two sweeps classify the
				// same refusals two ways is how they came to disagree by
				// twenty-eight targets (E483).
				//
				// `invalidEarthfile` matched "parse error" and nothing else, so
				// a target with no base image - which no engine can make valid -
				// counted as work left to do.
				case !errors.Is(err, interp.ErrRefused) && !errors.Is(err, interp.ErrNotProvided):
					invalid++

					continue

				case errors.Is(err, interp.ErrOnPurpose):
					onPurpose++

					continue
				}

				// Only the engine's own refusals are tallied.
				//
				// The list is read to decide what to build next, and it had
				// filled with this sweep's conditions until the top eight showed
				// one engine gap and seven things the harness withheld - a work
				// list whose visible entries were nobody's work (E421).
				refused[what]++
			}
		}()
	}

	type row struct {
		what string
		n    int
	}

	rows := make([]row, 0, len(refused))
	for what, n := range refused {
		rows = append(rows, row{what: what, n: n})
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n })

	var top []string

	for i, r := range rows {
		if i == 8 {
			break
		}

		top = append(top, fmt.Sprintf("%s x%d", r.what, r.n))
	}

	// The denominator is the point. "257 plan" is a number that can only go up
	// and says nothing about how far there is to go; "257 of N" is a parity
	// figure, and the refusals under it are the work, named.
	// Three numbers, because two of them mean different things. `planned/total`
	// is what this sweep achieved; `planned/(total-sweeps)` is what the engine
	// can do with input the sweep set up properly, and only the second is a
	// parity figure (E413).
	// Neither this sweep's conditions nor the files that are meant to be
	// refused: what is left is targets the engine could plan and did not.
	judged := total - sweeps - invalid - declaredFailing - onPurpose

	t.Logf("%d of %d targets plan; %d of %d after discounting this sweep's own"+
		" conditions (%d), invalid Earthfiles (%d), targets the tree drives"+
		" with --should_fail (%d) and constructs refused on purpose (%d),"+
		" across %d .earth files\n  what the engine refused: %s",
		planned, total, planned, judged, sweeps, invalid, declaredFailing, onPurpose,
		len(found), strings.Join(top, ", "))

	ratchetSlice(t, "earthtests", planned, plans...)
}

// relTo is a path as the corpus names it, for a list two machines will diff.
func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}

	return rel
}

// readTree reads the corpus's own `tests/Earthfile`, or an empty string.
//
// Empty rather than fatal: a checkout without the tree still has the `.earth`
// files, and the sweep's answer is only *less* discounted without it - which is
// the safe direction for a number that measures work left.
func readTree(t *testing.T, root string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join(root, "tests", "Earthfile"))
	if err != nil {
		return ""
	}

	return string(b)
}
