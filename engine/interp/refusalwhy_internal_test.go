package interp

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every recorded meaning is a sentence, and none of them is the flag again.
//
// The table this checks is small enough to read and therefore small enough to
// go stale unread. Two failure modes are worth a mechanical guard, because both
// produce a message that looks helpful and says nothing:
//
//   - an entry that repeats its own key, the "the --ssh flag enables ssh"
//     shape, which is how the external test's `--ssh` case came to pass before
//     anything was implemented;
//   - an entry with a full stop or a leading capital, which reads as a second
//     sentence in a message whose other lines are clauses.
//
// The descriptions are quoted from docs/earthfile/earthfile.md. Anything not in
// there has no entry, on purpose: see TestARefusalWithNoRecordedMeaningIsStillWellFormed.
func TestEveryRecordedFlagMeaningSaysSomething(t *testing.T) {
	t.Parallel()

	if len(flagMeanings) == 0 {
		t.Fatal("no flag meanings are recorded at all")
	}

	for flag, meaning := range flagMeanings {
		if !strings.HasPrefix(flag, "--") {
			t.Errorf("%q is not a flag", flag)
		}

		if meaning == "" {
			t.Errorf("%s has an empty meaning", flag)

			continue
		}

		// The flag's own words carry no information, so take them out and see
		// what is left. Three words is not a quality bar - it is the difference
		// between an explanation and an echo, and "enables ssh" fails it while
		// "gives the command the host's ssh agent" does not.
		//
		// Written as a subtraction rather than as "the meaning must not contain
		// the flag's words", which was the first version and which `--ssh` and
		// `--aws` both fail while explaining themselves perfectly well: some
		// flags are named after the thing they do, and the name is then the
		// only word for it.
		bare := strings.FieldsSeq(strings.ReplaceAll(strings.TrimPrefix(flag, "--"), "-", " "))
		drop := map[string]bool{}

		for w := range bare {
			drop[w] = true
		}

		left := 0

		for w := range strings.FieldsSeq(strings.ToLower(meaning)) {
			if !drop[strings.Trim(w, ",'`$")] {
				left++
			}
		}

		if left < 3 {
			t.Errorf("%s is explained with its own name and little else: %q", flag, meaning)
		}

		if strings.HasSuffix(meaning, ".") {
			t.Errorf("%s ends in a full stop, where the message has clauses: %q", flag, meaning)
		}

		if meaning != strings.TrimSpace(meaning) {
			t.Errorf("%s has whitespace at an end: %q", flag, meaning)
		}
	}
}

// undocumentedFlags are refused by name and deliberately have no meaning.
//
// The map's rule is that a description not taken from
// docs/earthfile/earthfile.md is worse than none, because a wrong one sends the
// reader somewhere there is nothing to find. These two are refused and are not
// in that document, so there is nothing to quote. The exemption is checked
// against the file below, so a flag that later gets documented fails and asks
// for an entry rather than sitting here forever.
var undocumentedFlags = map[string]string{
	"--chown":    "COPY --chown is a Dockerfile flag Earthfiles accept; earthfile.md does not describe it",
	"--cache-id": "WITH DOCKER --cache-id is undocumented; earthfile.md describes CACHE --id, a different flag",
}

// Every flag this engine refuses by name says what it was.
//
// The test above checks that recorded meanings are good. Nothing checked they
// are *complete*, and six of fourteen refused flags had none - `--from`,
// `--chmod`, `--network`, `--oidc`, `--chown`, `--cache-id` - so those refusals
// named the refusal and not the thing refused, which is the E68 shape this
// table exists to fix, applied to eight of the fourteen places it holds.
//
// The refusal sites are read from source because that is where they are: each
// is a `{opts.X, "--flag"}` row in a table local to the function that refuses.
// A guard over a hand-kept list of sites would go stale the first time somebody
// adds a row, which is the failure it exists to prevent.
func TestEveryRefusedFlagSaysWhatItWas(t *testing.T) {
	t.Parallel()

	flags := refusedFlags(t)

	// The pattern has been too narrow four times in this work. Fourteen is what
	// the tables hold today; markedly fewer means the scan broke, not that the
	// engine started explaining itself.
	// **Fifteen, down from sixteen**, and downward is the direction that needs
	// saying: `--cache-id` stopped being refused because it is now implemented
	// (E354). A ratchet on refusals counts the flags this engine has to say no
	// to, so it falls when one is supported and rises when a table grows - and
	// either movement is a decision somebody made, which is why it is written
	// here rather than inferred.
	//
	// A ratchet, not a guess: fifteen is what the tables hold today. It is a
	// weak check on its own - when `--force` moved to its own call the count
	// fell fifteen to fourteen and this still passed, because the number was
	// met by arithmetic rather than by finding the flag. The direction below is
	// the one that catches that.
	//
	// It was left at fifteen while the tables grew to sixteen, which is the
	// same failure one notch quieter: a floor a flag below the truth lets
	// exactly one flag leave the scan unnoticed. Measured - naming `--ssh`
	// through a constant, which the comment above says is invisible here,
	// passed at fifteen and fails at sixteen (E200). Raise this when a flag is
	// added, or the slack comes back.
	// Fourteen since `--chown` was implemented (E419) - a floor moves *down*
	// only when a refusal genuinely goes away, and the way to tell is that the
	// flag now has behaviour and a test of it. Lowered without that, this
	// mechanism stops catching a scan that has quietly stopped finding things.
	//
	// Thirteen since `--allow-privileged` was accepted (E476), which is the
	// other way a refusal genuinely goes away: the flag grants a permission
	// this engine never takes up, so there is nothing left to refuse and
	// `TestAllowPrivilegedDoesNotMakeAStepPrivileged` is the test of it. Four
	// refusal sites went with it, in BUILD, FROM, DO and WITH DOCKER.
	// Twelve since `--auto-skip` was accepted (E484), on the same terms as
	// `--allow-privileged` before it: the flag asks for a faster route to the
	// answer this engine already gives, so there is nothing left to refuse and
	// `TestTheAutoSkipOptionIsAccepted` is the test of it.
	// Eleven since `RUN --mount` stopped being refused as a flag: a Dockerfile
	// mount is now written back into the Earthfile spelling and refused by
	// *kind* where the kind is absent, so the bare flag is refused nowhere and
	// the message names `type=bind` rather than `--mount`. That is a better
	// refusal, not a missing one - TestADockerfileBindMountIsRefusedByKind is
	// the test of it - and this scan counts flags, so the number moved.
	// 11 -> 10: `--chmod` is implemented, so it is refused nowhere and the scan
	// no longer finds it. A flag leaving this list because the engine grew is
	// the good direction, and the floor moves with it rather than being kept
	// where a green run would need a refusal nobody wants back.
	// 10 -> 9: `--aws` likewise. It is gated by `VERSION --run-with-aws` now
	// rather than refused, and a gate is not a refusal - `features.needs` says
	// what the file must declare, which is a different sentence from this scan's.
	if len(flags) < 9 {
		t.Fatalf("only %d refused flags found (%v), so the scan is wrong rather"+
			" than the source", len(flags), flags)
	}

	doc := readDoc(t)

	for _, flag := range flags {
		why, exempt := undocumentedFlags[flag]

		switch {
		case flagMeanings[flag] != "":
			// Explained. And it must not *also* be exempt, which would mean two
			// entries disagreeing about whether the flag is documented.
			if exempt {
				t.Errorf("%s has a meaning and is listed as undocumented", flag)
			}

		case exempt:
			// The exemption is a claim about the documentation, so it is checked
			// against it.
			if strings.Contains(doc, "`"+flag) {
				t.Errorf("%s is exempt as undocumented (%s) but earthfile.md"+
					" describes it - quote it into flagMeanings", flag, why)
			}

		default:
			t.Errorf("%s is refused by name and says nothing about what it was;"+
				" quote earthfile.md into flagMeanings, or list it in"+
				" undocumentedFlags with the reason", flag)
		}
	}
}

// refusedFlags reads the refusal tables out of this package's source.
func refusedFlags(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	// Two shapes, because refusals come two ways and the first version of this
	// scan knew only one. A table row `{opts.Whatever, "--flag"}`, and a direct
	// call naming the construct, `notInLanguage("COPY --from", …)`. Moving
	// `--from` from the first shape to the second dropped it out of the scan,
	// and the count guard below is what said so - which is the whole reason it
	// is there.
	//
	// A flag named through a constant rather than a literal is invisible here:
	// `{opts.AllowPrivileged, allowPrivilegedFlag}` is not found. It has an
	// entry, so nothing is missed today, and the count guard turns a future
	// regression into a failure rather than a silence.
	shapes := []*regexp.Regexp{
		regexp.MustCompile(`\{opts\.[A-Za-z]+[^,{}]*,\s*"(--[a-z-]+)"\}`),
		// `\s*` after the paren because a refusal long enough to wrap puts the
		// string on the next line, which is exactly what happened to
		// `--network`: two widenings, one per accident, each caught by the
		// reverse check rather than by reading the regexp.
		//
		// The trailing `=?` is for a flag refused with its value:
		// `unsupported("RUN --network="+opts.Network, …)`. Without it the scan
		// missed `--network` entirely and the reverse check below - correctly -
		// reported its meaning as unreachable text.
		regexp.MustCompile(`(?:unsupported|notInLanguage|refusedOnPurpose)\(\s*"[A-Z][A-Z ]*(--[a-z-]+)=?"`),
	}

	seen := map[string]bool{}

	var out []string

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}

		for _, shape := range shapes {
			for _, m := range shape.FindAllStringSubmatch(string(b), -1) {
				if !seen[m[1]] {
					seen[m[1]] = true

					out = append(out, m[1])
				}
			}
		}
	}

	sort.Strings(out)

	return out
}

// readDoc is the reference for the language this engine implements.
func readDoc(t *testing.T) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Clean("../../docs/earthfile/earthfile.md"))
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// namedByConstant are refused through a constant rather than a string literal,
// so the source scan cannot see them.
//
// One flag, and it is worth an exemption rather than a cleverer pattern: the
// scan reads literals because that is what the tables hold, and following an
// identifier to its definition is a parser, not a regexp.
var namedByConstant = map[string]bool{"--allow-privileged": true}

// No meaning describes a flag that is not refused.
//
// The other direction, and the one that matters. The count floor above passed
// when `--force` moved out of the tables into its own call - fifteen became
// fourteen, the floor was fourteen, and a lost refusal site read as a met
// threshold. **A test that can be satisfied by arithmetic is not checking the
// thing it names.**
//
// Two entries also turned out to describe flags this engine *accepts*:
// `--keep-own` and `--symlink-no-follow` are honoured, so their descriptions
// could never be printed. Written and unreachable, the same class as a build tag
// on a test nobody notices is not running.
func TestNoMeaningDescribesAFlagThatIsNotRefused(t *testing.T) {
	t.Parallel()

	refused := map[string]bool{}
	for _, f := range refusedFlags(t) {
		refused[f] = true
	}

	for flag := range flagMeanings {
		if refused[flag] || namedByConstant[flag] {
			continue
		}

		t.Errorf("%s has a recorded meaning and is refused nowhere, so the text"+
			" can never be printed: delete it, or say here why the scan cannot"+
			" see the site", flag)
	}
}
