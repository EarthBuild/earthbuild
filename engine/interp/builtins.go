package interp

import (
	"os"
	"runtime"
	"strings"

	"github.com/EarthBuild/earthbuild/internal/version"
)

// UserPlatform is the machine that invoked the build.
//
// Exported because it is the one of the three platforms that is a property of
// where the engine is *running* rather than of what it was asked to do, so a
// test comparing against it cannot hard-code an answer that is right on one
// machine and wrong on the next.
func UserPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// builtinArgs are the values `ARG TARGETARCH` and its siblings declare.
//
// Three platforms, and they are genuinely three. On a Mac building for Linux
// the reference reports:
//
//	TARGET linux/arm64    what is being built
//	USER   darwin/arm64   the machine that typed the command
//	NATIVE linux/arm64    the machine doing the work
//
// TARGET and NATIVE coincide until a `--platform` says otherwise, which is
// exactly why they are easy to conflate and why the table is written out rather
// than derived from one value.
//
// They are supplied on *declaration* only. `$TARGETARCH` with no `ARG` above it
// expands to nothing in the engine that ships - checked against it directly -
// and filling it in would change what an Earthfile means. A compatible engine
// does not get to be helpful about that.
func builtinArgs(target, native, name, dir string, locally, push bool) map[string]string {
	// The name arrives with its `+` when the caller wrote one - `earth +build`
	// and `interp.Build(src, "build")` both reach here - so it is stripped once
	// and added back where the reference wants it. Without this,
	// `EARTH_TARGET` came out `++build` and `EARTH_TARGET_NAME` kept a `+` that
	// no comparison in any Earthfile expects.
	name = strings.TrimPrefix(name, "+")

	out := map[string]string{
		// The `EARTH_*` family the build itself knows.
		//
		// Supplied on declaration like the platform ones and for the same
		// reason, which their comment above states: an undeclared
		// `$EARTH_TARGET_NAME` expands to nothing in the reference, and filling
		// it in would change what an Earthfile means.
		//
		// This family was missing entirely, which `tests/empty-git.earth` found
		// by failing at execution on `test "" == "+test-empty"` - a gap the
		// planning sweep could not see, because the plan was correct (E423).
		"EARTH_TARGET_NAME": name,
		// The reference as this file would write it. A target elsewhere is
		// reached by a path, and this engine plans one file at a time, so the
		// unqualified form is the one a step can act on.
		"EARTH_TARGET": "+" + name,
		// Filled in below where a git origin qualifies it.
		"EARTH_TARGET_PROJECT": "",
		"EARTH_LOCALLY":        boolArg(locally),
		// Whether this is a CI build. **One of two words, never empty**: an
		// Earthfile branches on it, and in a shell an empty string reads exactly
		// like "not set", so a build on a CI machine would take the local path
		// with nothing to say why. `tests/ci-arg.earth` asserts the pair
		// directly (E443).
		"EARTH_CI": boolArg(inCI()),
		// The timestamp a reproducible build stamps its files with, defaulting
		// to 0 - which is what makes two builds of one tree produce the same
		// bytes. Left empty, every file written carries whatever the clock said.
		"EARTH_SOURCE_DATE_EPOCH": sourceDateEpoch(),
		// Which engine built this, and from what. An Earthfile that stamps a
		// label with either gets an empty label otherwise - provenance missing,
		// reported as a success (E448).
		// Whether this invocation is pushing, which this engine never is: `RUN
		// --push` is planned away and `SAVE IMAGE --push` is recorded and not
		// acted on. `false` is a fact about this invocation rather than a
		// placeholder, and when there is a push mode this is where its answer
		// comes from (E472).
		// `ARG EARTHLY_PUSH` is how an Earthfile asks what kind of build it is
		// in, and `tests/dotenv.earth` has a target per answer. It was `false`
		// outright, because there was no push mode for it to report.
		"EARTH_PUSH":      boolArg(push),
		"EARTH_VERSION":   engineVersion(),
		"EARTH_BUILD_SHA": engineBuildSHA(),
	}

	// What the build context's repository says about itself.
	//
	// **Always present, empty where there is no repository.** Every one is
	// documented that way, and the names have to exist whatever the answer: an
	// Earthfile declaring `ARG EARTHLY_GIT_HASH` outside a checkout gets an
	// empty string, and a name that were absent instead would make the
	// declaration an error rather than an empty label.
	//
	// This family was missing entirely, and the symptom was a binary: `earth
	// +earthly` produced one stamped `Version=dev-` and `GitSha=`, forty bytes
	// smaller than the same target built by the reference engine and otherwise
	// identical (E563).
	g := gitFactsFor(dir)

	out["EARTH_GIT_HASH"] = g.hash
	out["EARTH_GIT_SHORT_HASH"] = g.shortHash
	out["EARTH_GIT_CONTENT_HASH"] = g.tree
	out["EARTH_GIT_BRANCH"] = g.branch
	out["EARTH_GIT_TAG"] = g.tag
	out["EARTH_GIT_COMMIT_TIMESTAMP"] = g.commitTime
	out["EARTH_GIT_COMMIT_AUTHOR_TIMESTAMP"] = g.authorTime
	out["EARTH_GIT_AUTHOR"] = g.authorMail
	out["EARTH_GIT_AUTHOR_EMAIL"] = g.authorMail
	out["EARTH_GIT_AUTHOR_NAME"] = g.authorName
	out["EARTH_GIT_ORIGIN_URL"] = g.origin
	out["EARTH_GIT_ORIGIN_URL_SCRUBBED"] = scrubbed(g.origin)
	out["EARTH_GIT_PROJECT_NAME"] = g.project

	// **A reference is qualified by the repository it is in, when it is in
	// one.** `tests/empty-git.earth` asserts both halves in one file: with an
	// origin `EARTHLY_TARGET` is `github.com/earthly/earthly+test-origin-no-hash`,
	// and in a repository with no remote it is `+test-empty` - because there is
	// nothing to qualify it with.
	//
	// The comment on the unqualified form argued that a step can only act on
	// it. True, and beside the point: these are informational, they reach image
	// tags and messages, and a build that cannot say which project it is is
	// missing the useful half.
	if g.qualifier != "" {
		out["EARTH_TARGET_PROJECT"] = g.qualifier
		out["EARTH_TARGET"] = g.qualifier + "+" + name
	}

	// The tag half of the canonical reference, which for a checkout is the
	// branch it is on: `github.com/org/repo:branch+target`. Empty outside a
	// repository, because there is no canonical form to take a tag from.
	out["EARTH_TARGET_TAG"] = g.branch
	out["EARTH_TARGET_TAG_DOCKER"] = dockerTag(g.branch)

	// The legacy spelling, which the reference still supplies.
	//
	// `ARG EARTHLY_TARGET` is deprecated in favour of `ARG EARTH_TARGET` and
	// still works, so an Earthfile written before the rename builds - and
	// `tests/empty-git.earth`, which is one, asserts on exactly these two names
	// (E423). Supplying only the new spelling would be a rename this project did
	// to *other people's* files.
	for _, n := range []string{
		"EARTH_TARGET_NAME", "EARTH_TARGET", "EARTH_TARGET_PROJECT", "EARTH_LOCALLY",
		"EARTH_CI", "EARTH_SOURCE_DATE_EPOCH",
		"EARTH_VERSION", "EARTH_BUILD_SHA", "EARTH_PUSH",
		"EARTH_TARGET_TAG", "EARTH_TARGET_TAG_DOCKER",
		"EARTH_GIT_HASH", "EARTH_GIT_SHORT_HASH", "EARTH_GIT_CONTENT_HASH",
		"EARTH_GIT_BRANCH", "EARTH_GIT_TAG",
		"EARTH_GIT_COMMIT_TIMESTAMP", "EARTH_GIT_COMMIT_AUTHOR_TIMESTAMP",
		"EARTH_GIT_AUTHOR", "EARTH_GIT_AUTHOR_EMAIL", "EARTH_GIT_AUTHOR_NAME",
		"EARTH_GIT_ORIGIN_URL", "EARTH_GIT_ORIGIN_URL_SCRUBBED",
		"EARTH_GIT_PROJECT_NAME",
	} {
		out["EARTHLY_"+strings.TrimPrefix(n, "EARTH_")] = out[n]
	}

	for prefix, p := range map[string]string{
		"TARGET": target,
		"NATIVE": native,
		"USER":   UserPlatform(),
	} {
		os, arch, variant := splitPlatform(p)

		out[prefix+"PLATFORM"] = p
		out[prefix+"OS"] = os
		out[prefix+"ARCH"] = arch
		out[prefix+"VARIANT"] = variant
	}

	return out
}

// splitPlatform reads "os/arch[/variant]".
//
// Deliberately not `platforms.Parse`: that normalises, and normalising is wrong
// here. `platforms.Parse("linux/arm64")` fills in a variant of "v8", and the
// reference reports an empty one - so an Earthfile saving to
// `build/$GOARCH$VARIANT/` would write arm64v8 where every other tool in the
// build writes arm64.
func splitPlatform(p string) (os, arch, variant string) {
	parts := strings.Split(p, "/")

	switch len(parts) {
	case 0, 1:
		return "", "", ""
	case 2:
		return parts[0], parts[1], ""
	default:
		return parts[0], parts[1], parts[2]
	}
}

// boolArg is how a builtin says yes or no.
//
// The strings the reference uses, not Go's - an Earthfile comparing against
// "true" is comparing against the language's spelling.
func boolArg(b bool) string {
	if b {
		return "true"
	}

	return "false"
}

// inCI reports whether this build is running under continuous integration.
//
// From the environment, because that is where the answer lives: every CI system
// this is likely to meet sets `CI`, and the convention is old enough to be
// relied on. `false`, `0` and empty all mean no - a shell setting `CI=false`
// means it, and treating "set to anything" as yes would make the variable
// impossible to turn off.
//
// **This is ambient state entering the plan**, which the specification calls ε
// and expects: it reaches the key the way every other argument does, through the
// expansion of the command that used it, so two builds under different answers
// are two different steps rather than one step with two results.
func inCI() bool {
	switch strings.ToLower(os.Getenv("CI")) {
	case "", "false", "0", "no":
		return false
	default:
		return true
	}
}

// sourceDateEpoch is the timestamp a reproducible build writes.
//
// `SOURCE_DATE_EPOCH` is the cross-project convention
// (<https://reproducible-builds.org/docs/source-date-epoch/>), and 0 is the
// default the reference reports. Not the clock: a default of "now" is the one
// value that guarantees two builds of the same tree differ.
//
// Passed through as written rather than parsed and reformatted. A value this
// engine did not understand would be a value it silently changed, and the step
// comparing against it is comparing against what the caller set.
func sourceDateEpoch() string {
	if v := os.Getenv("SOURCE_DATE_EPOCH"); v != "" {
		return v
	}

	return "0"
}

// engineVersion names the engine, and never says nothing.
//
// The string is injected at link time, so it is empty in a `go test` binary, in
// `go run`, and in any build somebody makes without the release flags - which is
// the case that matters most, because **a value that is only correct in a
// release build is wrong every time a developer looks at it**. `tests/
// builtin-args.earth` asserts only that it is non-empty, and an empty one is the
// answer this engine was giving.
//
// The fallback names what is true rather than inventing a number: this is a
// build of the native engine that nobody stamped.
func engineVersion() string {
	if version.Version != "" {
		return version.Version
	}

	return "earthbuild-native (unstamped build)"
}

// engineBuildSHA is the commit this engine was built from.
//
// Same reasoning as engineVersion, and the same shape of fallback: "unknown" is
// a fact about this binary, and the empty string is a fact about nothing.
func engineBuildSHA() string {
	if version.GitSha != "" {
		return version.GitSha
	}

	return "unknown"
}

// addCIRunner adds the builtin `EARTHLY_CI_RUNNER`, which exists only where the
// dialect asked for it - see features.ciRunner.
//
// The value is a fact about the machine, taken from the environment: a CI runner
// sets it, and where nothing set it the honest answer is `false` rather than
// empty. `tests/builtin-args.earth` asserts the pair from both directions
// (E472).
func addCIRunner(into map[string]string) {
	value := os.Getenv("EARTHLY_CI_RUNNER")
	if value == "" {
		value = boolArg(false)
	}

	for _, name := range []string{"EARTH_CI_RUNNER", "EARTHLY_CI_RUNNER"} {
		into[name] = value
	}
}

// dockerTag is a reference's tag as a docker tag: valid, and never empty.
//
// A tag has to be usable where an image is named, and a branch is not: `/` is
// how a registry separates a repository from its host, so `john/work` in a tag
// names something else entirely. `latest` where there is no tag at all, which
// is what a reference with no canonical form is documented to give and what
// every other tool means by an unnamed version.
func dockerTag(tag string) string {
	if tag == "" {
		return "latest"
	}

	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, tag)

	// A docker tag may not begin with a separator, and a branch may.
	return strings.TrimLeft(safe, "._-")
}
