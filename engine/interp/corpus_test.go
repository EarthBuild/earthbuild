package interp_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// corpus finds the Earthfiles in this repository.
//
// Real input, written years before this engine existed and with no knowledge of
// it - which is the only kind that finds what an author's own examples never
// will.
func corpus(t *testing.T) []string {
	t.Helper()

	// The corpus is this repository. EARTH_CORPUS_DIR names it explicitly so the
	// test binary can run somewhere other than the package directory - a Linux
	// container with the tree mounted, for one. Depending on the working
	// directory would make the test silently cover nothing there.
	root := os.Getenv("EARTH_CORPUS_DIR")
	if root == "" {
		root = trackedCopy(t)
	}

	var found []string

	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner of the tree is not this test's problem
		}

		if fi.IsDir() && (fi.Name() == "node_modules" || fi.Name() == ".git") {
			return filepath.SkipDir
		}

		if !fi.IsDir() && fi.Name() == testEarthfile {
			found = append(found, p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	sort.Strings(found)

	return found
}

// TestCorpusIsAcceptedOrRefusedActionably is the claim a partial engine has to
// make: for every real Earthfile, it either builds a plan or **says what it
// cannot do and what to use instead** (green paper I10).
//
// It never panics, and it never fails with a bare message. Those are the two
// outcomes that make a partial engine worse than no engine: one loses the build,
// the other leaves a user with no idea whether the fault is theirs.
// localRemotes resolves a remote reference from a checkout already on this
// machine, when there is one.
//
// The corpus plans without a network, and must: it runs on every change. But
// one remote reference gates several hundred targets, and leaving that
// unmeasured means the biggest number in the report is a guess. Where the
// repository happens to be checked out beside this one, the reference is
// resolved for real - the actual Earthfiles, at the actual revision - and where
// it is not, the corpus reports what it can see and says so.
func localRemotes(t *testing.T) interp.Remotes {
	t.Helper()

	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	return func(repo, _ string) (string, error) {
		dir := filepath.Join(home, "git", strings.TrimPrefix(repo, "github.com/"))
		_, err := os.Stat(filepath.Join(dir, ".git"))
		if err != nil {
			// Actionable, because the corpus insists every refusal is: this one
			// is about the machine the corpus is running on rather than about
			// the Earthfile, and saying so is the difference between a reader
			// looking for a bug and a reader cloning a repository.
			//
			// Wrapped as a withheld capability, because that is exactly what it
			// is: this fetcher declines to reach the network, and the engine
			// has nothing missing. Left unwrapped it was three of the five
			// remaining causes on a work list read to decide what to build.
			return "", fmt.Errorf("%q is not checked out on this machine: %w"+
				"\n  the corpus resolves remote references from %s, so clone it there"+
				"\n  or use --engine=buildkit, which fetches them itself",
				repo, interp.ErrNotProvided, filepath.Dir(dir))
		}

		return dir, nil
	}
}

// wholeCheckout is how many Earthfiles a complete tree has, near enough.
//
// A floor rather than an equality: Earthfiles come and go and this should not
// need editing for each one. Comfortably above what `+code` carries (83) and
// comfortably below what the repository holds (192), so it separates the two
// cases it exists to separate and nothing else.
const wholeCheckout = 150

func TestCorpusIsAcceptedOrRefusedActionably(t *testing.T) {
	t.Parallel()

	// Skipped under -short, which is how the race-instrumented run stays
	// usable: these walk every Earthfile in the repository, and instrumentation
	// multiplies that by about ten. They run in full on every ordinary pass.
	if testing.Short() {
		t.Skip("corpus sweep")
	}

	fetch := localRemotes(t)

	files := corpus(t)

	// Refuse to pass on an empty corpus. A coverage test that quietly finds
	// nothing reports success while testing nothing, which is the failure mode
	// this whole file exists to avoid.
	//
	// Absent is not the same as wrong, and the two get different answers.
	// Failing on a machine that cannot hold the corpus reports a defect nobody
	// there can fix (E52); asking for a corpus that is not where you said it
	// was is still an error, because that is a request that failed.
	//
	// The threshold is "a whole checkout", not "some Earthfiles", and the
	// difference is not pedantry. `+code` carries a subset of the repository, so
	// an Earthfile in it that says `FROM ../..+base` refuses with *no Earthfile
	// for this reference* - 27 causes and 35 targets of it - and the report
	// blames the engine for the shape of the build image. A partial corpus does
	// not measure a smaller version of the same thing; it measures something
	// else (E158).
	if len(files) < wholeCheckout {
		if dir := os.Getenv("EARTH_CORPUS_DIR"); dir != "" {
			t.Fatalf("EARTH_CORPUS_DIR is %q and holds only %d Earthfiles", dir, len(files))
		}

		t.Skipf("found %d Earthfiles, fewer than the %d a whole checkout has;"+
			" this measurement is about a repository and this is part of one", len(files), wholeCheckout)
	}

	var (
		ok int
		// okDocker is the same count restricted to Earthfiles using WITH DOCKER
		// - a slice of the total, not a second measurement of it.
		okDocker int
		// dockerFiles is how many distinct such files contributed, kept only
		// because the first reading of okDocker came out exactly equal to the
		// number of Earthfiles in the corpus and a coincidence that neat is
		// worth one line to disprove.
		dockerFiles = map[string]bool{}
		refused     = map[string]int{}
		// causeSites[construct] is the set of source locations that construct was
		// actually refused at, so one line inherited by two hundred targets counts
		// once.
		causeSites = map[string]map[string]bool{}
		unusable   []string
		// unimplemented are the constructs this engine cannot do yet, as
		// opposed to the ones it is refusing because the input is invalid.
		unimplemented = map[string]bool{}
		// needsRunner are the constructs that are finished and simply cannot be
		// answered by a caller who planned without anywhere to run things or
		// anything to fetch with - which is exactly how this test plans.
		needsRunner = map[string]bool{}
		// declined are constructs this engine refuses by decision. Neither work
		// nor invalid input: listing them as either is how a position gets
		// implemented by somebody tidying up the first list, or ignored at the
		// bottom of the second.
		declined = map[string]bool{}
	)

	for _, f := range files {
		src, err := os.ReadFile(f) //nolint:gosec // a file in this repository
		if err != nil {
			continue
		}

		// Panics are the outcome under test as much as errors are, so each file
		// is evaluated in its own function with a recover.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked: %v", f, r)
				}
			}()

			for _, target := range targetsIn(string(src)) {
				opts := []interp.Option{interp.WithContext(filepath.Dir(f))}
				if fetch != nil {
					opts = append(opts, interp.WithRemotes(fetch))
				}

				_, err := interp.Build(string(src), target, opts...)
				if err == nil {
					ok++

					// Counted separately as well as together. 42 of this
					// repository's Earthfiles use WITH DOCKER, and a regression
					// confined to them would move the total by a few percent -
					// well inside the noise a person reads past. An aggregate
					// that can hide a whole construct's regression is an
					// aggregate measuring the wrong thing (E389).
					if strings.Contains(string(src), "WITH DOCKER") {
						okDocker++

						dockerFiles[f] = true
					}

					continue
				}

				construct, actionable := classify(err)
				refused[construct]++

				// An engine limitation offers another engine; a statement that
				// the Earthfile is wrong does not, because there is nothing to
				// switch to that would make invalid input valid. The two are
				// different kinds of number and adding them up has been
				// overstating the work left.
				switch {
				case errors.Is(err, interp.ErrNotProvided):
					needsRunner[construct] = true
				// errors.Is, not the presence of "--engine=buildkit" in the
				// text: a refusal made on purpose names the other engine too,
				// as a disclosure, and matching the phrase counted a decision
				// as work to do. Only a gap is work.
				case errors.Is(err, interp.ErrOnPurpose):
					declined[construct] = true
				case errors.Is(err, interp.ErrUnimplemented):
					unimplemented[construct] = true
				}

				if causeSites[construct] == nil {
					causeSites[construct] = map[string]bool{}
				}

				causeSites[construct][rootCause(err.Error())] = true

				if !actionable {
					unusable = append(unusable, f+" ["+target+"]: "+firstLine(err.Error()))
				}
			}
		}()
	}

	// Two readings, because they answer different questions and confusing them
	// is easy.
	//
	// A refusal deep in a chain of references is inherited by everything that
	// reaches it: one remote FROM in one file accounted for 182 refused targets,
	// through four levels of FROM. Counting refusals ranks by *blast radius* -
	// the right measure for choosing what to fix next, since one line unblocks
	// 182 targets - and badly overstates how much is left to do.
	//
	// Counting distinct *causes* answers the second question: how many separate
	// things are actually unimplemented.
	// Field names at the literal below: `n` and `causes` are both ints and
	// adjacent, so a positional form would swap two counts silently and the
	// report would be wrong in a way that reads perfectly (E187).
	type row struct {
		what   string
		n      int
		causes int
	}

	rows := make([]row, 0, len(refused))
	for what, n := range refused {
		rows = append(rows, row{what: what, n: n, causes: len(causeSites[what])})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].causes != rows[j].causes {
			return rows[i].causes > rows[j].causes
		}

		return rows[i].n > rows[j].n
	})

	distinct := 0
	for _, sites := range causeSites {
		distinct += len(sites)
	}

	var work, rejected, probes, declinedN, workCauses, rejectedCauses, probeCauses, declinedCauses int

	for _, r := range rows {
		switch {
		case declined[r.what]:
			declinedN += r.n
			declinedCauses += r.causes
		case needsRunner[r.what]:
			probes += r.n
			probeCauses += r.causes
		case unimplemented[r.what]:
			work += r.n
			workCauses += r.causes
		default:
			rejected += r.n
			rejectedCauses += r.causes
		}
	}

	t.Logf("%d targets planned, across %d Earthfiles (%d of them in Earthfiles"+
		" using WITH DOCKER, from %d such files)",
		ok, len(files), okDocker, len(dockerFiles))

	// **And the number is committed**, because this test cannot see the
	// difference that matters most. Its property is that every Earthfile is
	// accepted *or refused actionably*, so a change turning eighty targets into
	// tidy refusals passes it - and 489 targets planned for months with nothing
	// asserting so (E353).
	//
	// Checked here rather than in a test of its own: this is where the count is
	// computed, and a second sweep computing it again would be a second
	// definition of what "plans" means.
	ratchet(t, ok, len(files))
	ratchetSlice(t, "docker", okDocker)
	t.Logf("%d blocked by %d unimplemented constructs; %d refused as invalid input, from %d causes",
		work, workCauses, rejected, rejectedCauses)

	t.Logf("%d blocked for want of something this caller withheld - a probe to run, "+
		"a repository to fetch, an argument or secret to pass - from %d causes",
		probes, probeCauses)

	t.Logf("%d refused by decision, from %d causes - not work, and not wrong",
		declinedN, declinedCauses)

	t.Logf("  -- unimplemented: this is the work --")
	t.Logf("  %6s %8s  %s", "causes", "targets", "construct")

	// One example per cause, because a construct name is not a place to go and
	// look. "FROM, 5 causes, 376 targets" was read three times as propagation
	// from something else without anyone checking, which is what a report that
	// names no line invites.
	for _, r := range rows {
		if unimplemented[r.what] && !needsRunner[r.what] && !declined[r.what] {
			for _, line := range causeReport(r.what, r.causes, r.n, causeSites[r.what]) {
				t.Log(line)
			}
		}
	}

	// Listed rather than dropped. A refusal that says the Earthfile is wrong is
	// only worth discounting if it is *right*, and today a pattern that could
	// not be stat'd was reported as a file missing from the build context - a
	// bug wearing the costume of a correct refusal. Keeping these visible is
	// what makes the discount honest.
	t.Logf("  -- refused as invalid input: verify these are right --")

	for _, r := range rows {
		if !unimplemented[r.what] && !needsRunner[r.what] && !declined[r.what] {
			for _, line := range causeReport(r.what, r.causes, r.n, causeSites[r.what]) {
				t.Log(line)
			}
		}
	}

	_ = distinct

	// Every remaining unimplemented cause, in full. The list is short enough now
	// that one example per construct hides more than it shows.
	for _, r := range rows {
		if !unimplemented[r.what] || needsRunner[r.what] {
			continue
		}

		for _, site := range sortedSites(causeSites[r.what]) {
			t.Logf("  remaining: %s", site)
		}
	}

	for _, u := range unusable {
		t.Errorf("refused without naming a construct or an alternative:\n  %s", u)
	}
}

// rootCause is the deepest location in an error chain.
//
// A refusal is reported as the path that reached it - `FROM +a (x:1): FROM +b
// (y:2): ... (z:3)` - and the thing to fix is at z:3. Grouping by the whole
// message counts each path separately; grouping by the last location counts the
// line.
func rootCause(msg string) string {
	line := firstLine(msg)

	last := strings.LastIndex(line, "(")
	if last < 0 {
		return line
	}

	end := strings.Index(line[last:], ")")
	if end < 0 {
		return line
	}

	site := line[last+1 : last+end]

	// The text after the deepest location, which is what was actually wrong.
	tail := line[last+end:]
	if _, after, ok := strings.Cut(tail, ": "); ok {
		return site + " " + after
	}

	return site
}

// innermost is the message at the end of a chain of references.
//
// A refusal reads `FROM +a (x:1): BUILD +b (y:2): IF at z:3 needs to run ...`,
// and the construct that could not be handled is the last one, not the first.
// Grouping by the first named every chain after the line that referred to it,
// so the report ranked `BUILD` and `FROM` above everything - which is a ranking
// of how targets reach a problem rather than of what the problem is, and sent
// two iterations of work at the wrong thing.
func innermost(line string) string {
	if i := strings.LastIndex(line, "): "); i >= 0 {
		return line[i+3:]
	}

	return line
}

func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}

	return s
}

// classify extracts what an error was about, and whether it is actionable.
//
// Actionable is a *property*, not a list of blessed phrases: the message says
// where the problem is - a line, a target, a path - and what to do about it,
// which is either a remedy or the other engine. An allowlist of known-good
// wordings tests the allowlist; this tests the messages.
func classify(err error) (string, bool) {
	msg := err.Error()
	line := innermost(firstLine(msg))

	// Where: a source location, a quoted name, or a named target.
	located := strings.Contains(line, "Earthfile:") ||
		strings.Contains(line, "\"") ||
		strings.Contains(line, "+")

	// What to do: an alternative engine, or a following line of guidance.
	remedied := strings.Contains(msg, "--engine=buildkit") ||
		strings.Contains(msg, "\n  ")

	switch {
	case strings.Contains(msg, "not supported by the native engine"):
		return strings.Fields(line)[0], remedied

	case strings.Contains(line, "cycle between targets"):
		return "cycle", remedied

	case strings.Contains(line, "parse the Earthfile"):
		return "parse error", true

	case strings.Contains(line, "build context"), strings.Contains(line, "is not in the build context"):
		return "missing context file", remedied

	case strings.Contains(line, "no target named"):
		return "unknown target", remedied

	case strings.Contains(line, "has no base image"),
		strings.Contains(line, "has no filesystem to copy into"),
		strings.Contains(line, "has no filesystem to take from"):
		// A genuinely invalid Earthfile - a target with no FROM anywhere, which
		// this repository has as test data. Refusing is correct; the message
		// must still say why.
		return "no base image", remedied
	}

	return line, located && remedied
}

// targetsIn lists the target names in a source, cheaply: the corpus is scanned
// for coverage, not parsed twice.
//
// Functions are excluded. A function is written exactly like a target - the
// grammar has `function-ref = target-ref`, and the only difference is a FUNCTION
// as its first command - so a scanner that takes every `name:` line asks the
// interpreter to build functions as targets and collects twenty-one refusals
// that say nothing about the engine.
func targetsIn(src string) []string {
	var (
		out     []string
		current int
	)

	lines := strings.Split(src, "\n")

	for i, line := range lines {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}

		name, rest, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || rest != "" || name == "" || strings.ContainsAny(name, " \t") {
			continue
		}

		if isFunction(lines[i+1:]) {
			continue
		}

		out = append(out, name)
		current++
	}

	return out
}

// isFunction reports whether a block's first command is FUNCTION.
func isFunction(rest []string) bool {
	for _, line := range rest {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if line[0] != ' ' && line[0] != '\t' {
			return false // the next block began, so this one was empty
		}

		return trimmed == "FUNCTION" || strings.HasPrefix(trimmed, "FUNCTION ")
	}

	return false
}

// sortedSites orders a construct's causes so the example shown is the same one
// on every run.
// causeReport renders one cause: its counts, and one place to go and look.
//
// One example rather than all of them, because a cause with eighty sites would
// bury the eighty other causes; and the *lowest-sorting* example rather than
// whichever the map yielded, so two runs over one corpus produce one report
// (I12).
//
// Used by both lists. It used to be written out in the unimplemented loop and
// not at all in the "verify these are right" loop, which meant the list asking
// to be verified was the one nobody could check - and that is where a wrong
// refusal hides, because it looks exactly like a right one.
func causeReport(what string, causes, targets int, sites map[string]bool) []string {
	out := []string{fmt.Sprintf("  %6d %8d  %s", causes, targets, what)}

	for _, site := range sortedSites(sites) {
		out = append(out, fmt.Sprintf("  %16s%s", "", site))

		break
	}

	return out
}

func sortedSites(sites map[string]bool) []string {
	out := make([]string, 0, len(sites))
	for site := range sites {
		out = append(out, site)
	}

	sort.Strings(out)

	return out
}

// trackedCopy is the repository as git has it, in a directory of its own.
//
// The sweep plans every target, and planning a `COPY .` digests the directory
// it names. The walk above skips `node_modules` when *finding* Earthfiles; the
// digest does not, because the Earthfile asked for the whole directory and that
// is the right answer to give it.
//
// So the sweep did work proportional to whatever build output happened to be
// lying about. A developer's `examples/` here holds 958 MB of jars, bundles and
// node_modules against 2.5 MB tracked, and the run went from about a minute to
// past twenty-five - on the machine where this test is meant to run, since it
// skips in CI for want of a whole checkout (E158b).
//
// Tracked files only, which is the same correction as that one from the other
// side: **a polluted corpus measures something else, just as a partial one
// does.** It also makes the count stable - 192 Earthfiles whatever the working
// tree looks like - so the whole-checkout floor means what it says.
//
// Falls back to the tree in place when git cannot answer, which is a checkout
// without a `.git` and is exactly the case EARTH_CORPUS_DIR exists for.
func trackedCopy(t *testing.T) string {
	t.Helper()

	out, err := exec.Command("git", "-C", "../..", "ls-files", "-z").Output()
	if err != nil {
		t.Logf("git cannot list this tree (%v), so the corpus is the working"+
			" directory and its cost depends on what is untracked in it", err)

		return "../.."
	}

	dst := t.TempDir()

	for rel := range strings.SplitSeq(string(out), "\x00") {
		if rel == "" {
			continue
		}

		src := filepath.Join("../..", rel)

		b, err := os.ReadFile(filepath.Clean(src))
		if err != nil {
			// A tracked file that is not there is a deleted-but-unstaged one,
			// which is a state a working tree is allowed to be in.
			continue
		}

		at := filepath.Join(dst, rel)

		err = os.MkdirAll(filepath.Dir(at), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(at, b, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return dst
}
