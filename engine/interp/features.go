package interp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// features are the constructs an Earthfile has opted into on its VERSION line.
//
// `VERSION --try 0.8` is not decoration: the flags say which dialect the file is
// written in, so that an Earthfile which builds somewhere builds everywhere. An
// engine that ignores them accepts files the reference refuses - and an
// Earthfile written against *this* engine then fails for everybody else, which
// is the quiet way a compatible implementation stops being one.
//
// Per file rather than per build, like IMPORT, because a VERSION line is a
// declaration a file makes about itself. A file that opts into nothing gets
// nothing, whatever the file that referred to it asked for.
//
// Deliberately a set of known names rather than "any flag is fine": an unknown
// flag is a file written for a dialect this engine does not have, and saying so
// is better than building it as though the flag were absent.
type features struct {
	// runWithAWS is `--run-with-aws`, which RUN --aws needs.
	runWithAWS bool
	try        bool
	passArgs   bool
	// projectSecrets is `--use-project-secrets`, which PROJECT arrived with. A
	// file older than the feature is using a keyword its dialect does not have,
	// and `tests/project-secrets-without-flag.earth` says so in the command it
	// runs (E461).
	projectSecrets bool
	// argScopeAndSet is `--arg-scope-and-set`, which SET needs. Gated because
	// the corpus has a file that uses SET without it and expects to be refused:
	// accepting the flag is a statement about the dialect, and *using the
	// construct* without it is an Earthfile that builds here and nowhere else
	// (E458).
	argScopeAndSet bool
	// functionKeyword is `--use-function-keyword`, after which `COMMAND` is the
	// old spelling and is refused. The gate runs the other way from the rest:
	// the flag makes something *illegal* rather than legal, because it renames a
	// keyword rather than adding one.
	functionKeyword bool
	// ciRunner is `--earthly-ci-runner-arg`, which adds the builtin argument of
	// that name. Gated because `tests/builtin-args.earth` asserts *both* halves:
	// the name is empty under a plain VERSION line and answered under this one,
	// so a builtin supplied regardless would answer a question the file never
	// asked (E472).
	ciRunner bool
	// rawOutput is `--raw-output`, which RUN --raw-output needs. Gated rather
	// than ignored because the flag changes what the *build prints*, and a file
	// whose fold markers land at the start of a line here and mid-line
	// elsewhere is written for one engine (E937).
	rawOutput bool
	// shellOutAnywhere is `--shell-out-anywhere`, on by default from 0.7. Before
	// it, a `$(...)` is expanded only as the whole value of an `ARG`, and a
	// failing one leaves the argument empty rather than stopping the build.
	shellOutAnywhere bool
}

// knownFeatures maps a VERSION flag to the field it sets.
//
// Only the ones this engine actually gates. A flag the reference has and this
// engine does not is listed here as understood-and-ignored rather than refused,
// because refusing it would reject a file over a feature the file may not even
// use.
var knownFeatures = map[string]func(*features){
	"--try": func(f *features) { f.try = true },
	// BUILD, FROM and COPY take `--pass-args`. Gated for the same reason
	// `--try` is - see defaultsFor for why the gate opens by itself at 0.8.
	"--pass-args": func(f *features) { f.passArgs = true },
	// SET, and the renaming of COMMAND to FUNCTION. Both gate a *construct*
	// rather than a flag, which is the half `ignoredFeatures` cannot do (E458).
	"--arg-scope-and-set":    func(f *features) { f.argScopeAndSet = true },
	"--use-project-secrets":  func(f *features) { f.projectSecrets = true },
	"--use-function-keyword": func(f *features) { f.functionKeyword = true },
	// The builtin argument of the same name, which is a value rather than a
	// construct - the third thing a feature flag can gate.
	"--earthly-ci-runner-arg": func(f *features) { f.ciRunner = true },
	// `RUN --aws`, which hands the invoking user's AWS credentials to a step.
	// A capability rather than a spelling, so a file that uses it says so.
	"--run-with-aws": func(f *features) { f.runWithAWS = true },
	// `RUN --raw-output`, which drops the prefix naming the step a line came
	// from. Gated at the construct as well as named here, which is what the
	// reference does.
	"--raw-output": func(f *features) { f.rawOutput = true },
	// A `$(...)` anywhere rather than only as a whole `ARG` value, and a failing
	// one reported rather than swallowed. See defaultsFor for the 0.7 boundary
	// and `tests/shell-out` for the four files that state it.
	"--shell-out-anywhere": func(f *features) { f.shellOutAnywhere = true },
}

// ignoredFeatures are flags this engine understands to exist and does not gate.
//
// Accepted and dropped: they enable constructs this engine either implements
// unconditionally or refuses by name elsewhere, and a file that names one is
// not written for a dialect we lack.
var ignoredFeatures = map[string]bool{
	// Refused at the construct instead: a wildcard target reference names the
	// feature and says this engine does not expand one (E412). A file that names
	// the flag and uses no wildcard builds, which is 24 targets in `tests/` that
	// the whole-file refusal was taking with it.
	"--wildcard-copy":   true,
	"--wildcard-builds": true,
	// Permission to write `BUILD --auto-skip`, which this engine refuses by
	// name. Accepting the flag is therefore a statement about the dialect and
	// not a claim to the feature (E414); eight targets in `tests/` were refused
	// at their VERSION line for an option they never used.
	"--build-auto-skip": true,
	// `.dockerignore` is read by this engine already, and unconditionally:
	// `engine/ignore` looks for `.earthignore`, then `.earthlyignore`, then
	// `.dockerignore`, the last "so a project that has one and no
	// Earthfile-specific one gets what it plainly meant". The reference gates
	// that behind this flag and only for a Dockerfile's context.
	//
	// So naming it is a statement about the dialect rather than a claim to a
	// feature - the first of the two conditions this table states. Refusing it
	// took a whole file down at its VERSION line over behaviour the file already
	// had, which is what `docker-build-integration` hit.
	"--use-docker-ignore":                   true,
	"--global-cache":                        true,
	"--use-cache-command":                   true,
	"--use-host-command":                    true,
	"--use-copy-link":                       true,
	"--referenced-save-only":                true,
	"--for-in":                              true,
	"--no-network":                          true,
	"--check-duplicate-images":              true,
	"--earthly-version-arg":                 true,
	"--wait-block":                          true,
	"--use-visited-upfront-hash-collection": true,
	// A WITH DOCKER cache, which this engine either provides or refuses by name
	// at the construct itself.
	"--docker-cache": true,
	// Both grant a *permission*, and this engine is stricter than the
	// permission either way round - so the flag can be ignored, because the
	// refusal still happens at the point of use.
	//
	// `--allow-without-earthly-labels` relaxes a check the reference makes on
	// images loaded into a WITH DOCKER, and this engine makes no such check.
	// `--allow-privileged-from-dockerfile` lets a FROM DOCKERFILE be
	// privileged, and this engine refuses privileged execution by name wherever
	// it appears - declaring the flag does not change that, which is asserted
	// rather than assumed.
	//
	// The safe direction of E34's asymmetry: refusing something already
	// implemented costs a working build, accepting something not implemented
	// costs a wrong one, and nothing here is accepted that was not already.
	// Between them they were blocking ten targets in this repository's own
	// tests/ tree.
	"--allow-without-earthly-labels":     true,
	"--allow-privileged-from-dockerfile": true,
	// Makes `SAVE ARTIFACT ... AS LOCAL` outside the project require `--force`.
	// This engine is stricter in both directions and unconditionally: such a
	// destination is refused (checkLocalDest), and `--force` itself is refused
	// by name with the reason written where it is refused. Turning the feature
	// on therefore changes nothing here, and the seven corpus invocations that
	// pass it get the answer they are asserting anyway (E473).
	"--require-force-for-unsafe-saves": true,
}

// defaultsFor turns on the features a version number implies.
//
// A flag is how a file opts into a dialect *before* that dialect is the
// default; once it is, files stop naming it and the engine must not start
// refusing them. `--pass-args` is the case that showed this: two Earthfiles
// here declare it at 0.7, and the repository's own root file uses
// `BUILD --pass-args` at 0.8 while declaring nothing - which the reference
// accepts and a gate on the flag alone refuses (E63).
//
// So the evidence for the boundary is in the repository rather than in a
// changelog, and it is written down here rather than inferred at each site.
func defaultsFor(version string) features {
	var f features

	// **0.7, and the corpus says so in six comments.** Every `old*.earth` in
	// `tests/shell-out` opens `VERSION 0.6 # do not change to 0.7; this test is
	// for old functionality`, and between them they state all of what changes:
	// a `$(...)` becomes expandable anywhere rather than only as a whole `ARG`
	// value, and a failing one stops the build rather than leaving the argument
	// empty. `new.earth` is 0.8 and asserts the other side (E957).
	if version >= "0.7" {
		f.shellOutAnywhere = true
	}

	// Only what is evidenced. `--try` is *not* here: five files in this
	// repository declare `VERSION --try 0.8`, so it is still opt-in at 0.8 and
	// turning it on by default would accept files the reference refuses - the
	// same fault in the other direction.
	if version >= "0.8" {
		f.passArgs = true
		// `COMMAND` was renamed to `FUNCTION` at 0.8, and the corpus says so in
		// its own comments: `tests/command.earth` opens with *"Do not update
		// this to 0.8 (function.earth is used for testing 0.8)"* and carries a
		// target that expects `FUNCTION` to fail, while `function.earth` at 0.8
		// carries the mirror for `COMMAND` (E459).
		//
		// A default rather than only a flag, for the reason above: a file stops
		// naming a flag once its version implies it, and an engine gating on the
		// flag alone accepts what the reference refuses.
		f.functionKeyword = true
		// PROJECT is ordinary by 0.8: `tests/project-secrets.earth` writes it
		// under a plain `VERSION 0.8`, and only the 0.6 file needs the flag.
		f.projectSecrets = true
		// LET and SET are ordinary from 0.8: `features.ArgScopeSet` carries
		// `enabled_in_version:"0.8"`, and the reference gates the construct on
		// that field and nothing else (`handleSet`).
		//
		// Gated on the flag alone until now, on a misreading of
		// `tests/arg-set.earth` - a `--should_fail` file that is *itself*
		// `VERSION 0.8`, so its refusal was never about the flag (E458). The
		// corpus computes with LET and SET wherever it counts anything, so the
		// gate did not refuse one construct: it left variables unset in every
		// target that used them.
		f.argScopeAndSet = true
	}

	return f
}

// versionOf is the version number on a VERSION line, ignoring its flags.
func versionOf(v *earthfile.Version) string {
	for _, arg := range v.Args {
		if !strings.HasPrefix(arg, "--") {
			return arg
		}
	}

	return ""
}

// readFeatures reads the flags from a VERSION line, then the caller's.
//
// `overrides` come from `--version-flag-overrides`, which turns a feature on for
// every file in the build without editing any of them. Applied *after* the line
// so that the caller wins, which is the direction the name says (E473).
//
// A file with no VERSION line takes neither: it is not an Earthfile this engine
// builds, and the refusal for that belongs to whoever asked for it rather than
// here.
func readFeatures(v *earthfile.Version, overrides []string) (features, error) {
	var f features

	if v == nil {
		return f, nil
	}

	f = defaultsFor(versionOf(v))

	for _, arg := range v.Args {
		if !strings.HasPrefix(arg, "--") {
			continue // the version number itself
		}

		err := applyFeature(&f, arg, "VERSION ")
		if err != nil {
			return f, err
		}
	}

	for _, arg := range overrides {
		// Written with or without its dashes. The corpus passes bare names -
		// `--version-flag-overrides=require-force-for-unsafe-saves` - and a
		// caller who copies the flag off a VERSION line writes them; the two
		// name the same feature, and telling the second that it does not exist
		// would be a diagnosis about punctuation.
		err := applyFeature(&f, "--"+strings.TrimPrefix(arg, "--"),
			"--version-flag-overrides ")
		if err != nil {
			return f, err
		}
	}

	return f, nil
}

// applyFeature turns on one flag, or says why it cannot.
//
// `from` names where the flag was written, because the two places differ in what
// the reader can do about it: a VERSION line is the file's, an override is the
// command's.
func applyFeature(f *features, arg, from string) error {
	// `--flag=value` is not a form any of these take, but splitting is cheaper
	// than being surprised by one.
	name, _, _ := strings.Cut(arg, "=")

	if set, known := knownFeatures[name]; known {
		set(f)

		return nil
	}

	if ignoredFeatures[name] {
		return nil
	}

	return fmt.Errorf(
		"%s%s is a feature this engine does not know"+
			"\n  it may be a newer flag, or a typo for one of: %s",
		from, name, strings.Join(knownNames(), ", "))
}

// knownNames lists the flags this engine gates on, for a diagnosis.
func knownNames() []string {
	out := make([]string, 0, len(knownFeatures))
	for name := range knownFeatures {
		out = append(out, name)
	}

	// Sorted, because this goes into an error message and a message is part of
	// what a build produces (I12). Straight out of the map it was stable while
	// there was one known feature and random the moment there were two - which
	// is what `TestPlanningIsDeterministic` had been catching about one run in
	// six ever since (E66).
	sort.Strings(out)

	return out
}

// needs refuses a construct the file did not opt into.
func (f features) needs(on bool, construct, flag, where string) error {
	if on {
		return nil
	}

	return fmt.Errorf(
		"%s at %s needs the %s feature"+
			"\n  the file's VERSION line does not ask for it: write `VERSION %s 0.8`",
		construct, where, flag, flag)
}
