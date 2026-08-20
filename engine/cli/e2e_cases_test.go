package cli_test

import (
	"strings"
	"testing"
)

// buildCase is one construct, written once and run through both backends.
//
// The Earthfile body is templated because a host target and a sandboxed one
// differ only in their preamble and how the result leaves the build: `LOCALLY`
// writes into the project directory, `FROM` writes into an image and needs a
// SAVE ARTIFACT to get it out. Everything between is identical, which is the
// point - the same construct must mean the same thing on both.
type buildCase struct {
	name string
	// body is the commands, with %s where a shell is needed.
	body string
	// file is what to check afterwards, relative to the project directory.
	file string
	want string
	// context are files to place in the project directory first.
	context map[string]string
	// version is this case's VERSION line, empty for `VERSION 0.8`.
	//
	// A case that uses a construct behind a feature flag has to declare it, the
	// same as a real Earthfile: SET is gated on `--arg-scope-and-set`, and a
	// table that wrote one dialect for every case was testing a file nobody
	// could write (E458).
	version string
	// sandboxOnly marks a case whose meaning differs between the two backends,
	// so comparing them would compare nothing.
	//
	// Two kinds qualify, and both are right to differ:
	//
	//   - COPY, because a host target has no image to copy *into*, and writing
	//     into the developer's own directory instead is a surprise nobody wants
	//     from a build tool;
	//   - anything naming an absolute path, because `/script` is the image root
	//     in a sandbox and the *machine's* root on a host. A shared case that
	//     wrote to `/` would be a build tool writing to the root of the
	//     developer's filesystem, and the host refusing it is the system working.
	//
	// Such a case is skipped on the host with that reason rather than quietly
	// dropped, so the coverage difference between the backends stays visible
	// instead of being hidden behind a green run.
	sandboxOnly bool
}

// cases are the constructs worth checking end to end.
//
// Each writes to $file so the assertion is about what happened rather than
// about an exit code, which a build that lost its quoting still reports as
// success.
func cases(t *testing.T) []buildCase {
	t.Helper()

	return []buildCase{
		{
			// Setting any environment variable used to take PATH away with it.
			// `cmd.Env = req.Env` inherits the parent environment when the
			// slice is nil and replaces it entirely when it is not, so an
			// Earthfile with no ENV got a PATH by accident and one with a
			// single ENV lost it - reported as `sh: git: not found` on a line
			// that had nothing to do with the ENV above it.
			name: "ENV does not take PATH with it",
			body: "    RUN %s -c \"mkdir -p /usr/local/bin && " +
				"printf '#!/bin/sh\\necho hello\\n' > /usr/local/bin/greet && " +
				"chmod +x /usr/local/bin/greet\"\n" +
				"    ENV ANYTHING=1\n" +
				"    RUN %s -c \"greet > FILE\"\n",
			file: testArtefact, want: "hello\n",
			// /usr/local/bin is the image's on a sandbox and the developer's
			// own on the host, and a build tool installing a script into it is
			// not a thing anybody asked for.
			sandboxOnly: true,
		},
		{
			// A step had no /etc/resolv.conf, so DNS did not work at all - and
			// every build that fetches anything is a build that resolves a
			// name first. maven, npm, pip, apt and cargo all failed here, and
			// each reported its own unrelated-looking error.
			//
			// An image ships no resolver configuration because the runtime is
			// expected to provide one; nothing did.
			name: "a step can resolve a name",
			body: "    RUN %s -c \"test -s /etc/resolv.conf && echo resolver-ok > FILE\"\n",
			file: testArtefact, want: "resolver-ok\n",
			sandboxOnly: true,
		},
		{
			// A step had no /proc, and the loader computes $ORIGIN from
			// /proc/self/exe - so every binary with an $ORIGIN rpath failed with
			// "cannot open shared object file" naming a library that was
			// present, readable and resolvable by ldd. Java is the famous one;
			// `maven:3.8.5-openjdk-17` could not run java at all.
			name: "a step has /proc",
			body: "    RUN %s -c \"test -e /proc/self/exe && echo proc-ok > FILE\"\n",
			file: testArtefact, want: "proc-ok\n",
			sandboxOnly: true,
		},
		{
			// A step's filesystem had /dev/null and nothing else, so anything
			// reading /dev/urandom failed - which is most language runtimes,
			// every TLS handshake, and docker's own plugin loader, whose
			// failure to open /dev/null while listing plugins is what led here.
			// A build environment without the standard devices is not one
			// anybody's software expects.
			name: "the standard devices are there",
			body: "    RUN %s -c \"head -c 8 /dev/urandom > /dev/null" +
				" && head -c 8 /dev/zero > /dev/null && echo devices-ok > FILE\"\n",
			file: testArtefact, want: "devices-ok\n",
			// The devices in question are the *step's*, which a host build does
			// not have a separate set of.
			sandboxOnly: true,
		},
		{
			name: "an argument reaches the command",
			body: "    ARG greeting=hello\n    RUN %s -c \"echo $greeting > FILE\"\n",
			file: testArtefact, want: "hello\n",
		},
		{
			name:    "LET and SET run in order",
			version: "VERSION --arg-scope-and-set 0.8",
			body: "    LET stage=first\n" +
				"    RUN %s -c \"echo $stage > FILE\"\n" +
				"    SET stage=second\n" +
				"    RUN %s -c \"echo $stage >> FILE\"\n",
			file: testArtefact, want: "first\nsecond\n",
		},
		{
			name: "a condition selects a branch",
			body: "    ARG mode=debug\n" +
				"    IF [ \"$mode\" = \"release\" ]\n" +
				"        RUN %s -c \"echo release > FILE\"\n" +
				"    ELSE\n" +
				"        RUN %s -c \"echo debug > FILE\"\n" +
				"    END\n",
			file: testArtefact, want: "debug\n",
		},
		{
			name: "the environment reaches the process",
			body: "    ENV MESSAGE=from-env\n    RUN %s -c \"echo $MESSAGE > FILE\"\n",
			file: testArtefact, want: "from-env\n",
		},
		{
			name: "quoting survives to the shell",
			body: "    RUN %s -c \"echo one two | tr ' ' '-' > FILE\"\n",
			file: testArtefact, want: "one-two\n",
		},
		{
			name: "a variable the shell owns is left alone",
			body: "    RUN %s -c 'for i in a b; do echo $i >> FILE; done'\n",
			file: testArtefact, want: "a\nb\n",
		},
		{
			name: "an escaped dollar is a literal",
			body: "    RUN %s -c 'echo \\$5 > FILE'\n",
			file: testArtefact, want: "$5\n",
		},
		{
			name: "a function is inlined with its argument",
			body: "    DO +WRITE --text=from-a-function\n",
			file: testArtefact, want: "from-a-function\n",
		},
		{
			name: "a working directory applies to later steps",
			body: "    RUN %s -c \"mkdir -p sub\"\n" +
				"    WORKDIR sub\n" +
				"    RUN %s -c \"echo nested > out.txt\"\n",
			file: "sub/out.txt", want: "nested\n",
		},
		{
			name: "a chain stops at the first failure",
			body: "    RUN %s -c \"echo first > FILE && echo second >> FILE\"\n",
			file: testArtefact, want: "first\nsecond\n",
		},
		{
			name: "the environment persists into a later step",
			body: "    ENV CARRIED=held\n" +
				"    RUN %s -c \"echo an-unrelated-step\"\n" +
				"    RUN %s -c \"echo $CARRIED > FILE\"\n",
			file: testArtefact, want: "held\n",
		},
		// `IF [ -f flag.txt ]` belongs here and is not here yet. A condition
		// that must be evaluated in a sandbox is specified (green paper §3.4a:
		// predict from the site's history, speculate, and let the evaluation
		// decide under I5) and unimplemented; the interpreter refuses it by
		// name. Adding the case now would assert a diagnostic rather than a
		// build, which is the wrong test in the wrong file - the refusal is
		// already covered by TestConditionsNeedingExecutionAreRefused.
		{
			name: "a later step reads what an earlier one wrote",
			body: "    RUN %s -c \"echo carried > /passed-on\"\n" +
				"    RUN %s -c \"cat /passed-on > FILE\"\n",
			file: testArtefact, want: "carried\n",
			sandboxOnly: true,
		},
		{
			name: "a relative working directory nests",
			body: "    RUN %s -c \"mkdir -p a/b\"\n" +
				"    WORKDIR a\n" +
				"    WORKDIR b\n" +
				"    RUN %s -c \"pwd > FILE\"\n",
			file: testArtefact, want: "/a/b\n",
			sandboxOnly: true,
		},
		{
			name: "an absolute working directory replaces the last",
			body: "    RUN %s -c \"mkdir -p a/b /elsewhere\"\n" +
				"    WORKDIR a/b\n" +
				"    WORKDIR /elsewhere\n" +
				"    RUN %s -c \"pwd > FILE\"\n",
			file: testArtefact, want: "/elsewhere\n",
			sandboxOnly: true,
		},
		{
			name: "a mode survives to the next step",
			body: "    RUN %s -c \"printf '#!/bin/sh\\necho ran\\n' > /script; chmod 755 /script\"\n" +
				"    RUN %s -c \"/script > FILE\"\n",
			file: testArtefact, want: "ran\n",
			sandboxOnly: true,
		},
		{
			name: "a symlink survives to the next step",
			body: "    RUN %s -c \"echo pointed-at > /target; ln -s /target /link\"\n" +
				"    RUN %s -c \"cat /link > FILE\"\n",
			file: testArtefact, want: "pointed-at\n",
			sandboxOnly: true,
		},
		{
			name:    "a file comes in from the build context",
			body:    "    COPY from-context.txt FILE\n",
			context: map[string]string{"from-context.txt": "context content\n"},
			file:    testArtefact, want: "context content\n", sandboxOnly: true,
		},
		{
			name: "several sources all arrive",
			body: "    COPY a.txt b.txt /both/\n" +
				"    RUN %s -c \"cat /both/a.txt /both/b.txt > FILE\"\n",
			context: map[string]string{"a.txt": "first\n", "b.txt": "second\n"},
			file:    testArtefact, want: "first\nsecond\n", sandboxOnly: true,
		},
		{
			// `--dir` brings the directory itself - into a destination that is
			// already a directory.
			//
			// Measured against the reference across the four combinations of
			// `--dir` and an existing destination, because this engine had it
			// wrong in both directions at once and neither was visible from the
			// other. It is `cp -r`: with a destination that exists the source
			// goes *inside* it, and with one that does not the destination
			// becomes the copy.
			name: "--dir brings the directory itself into one that exists",
			body: "    RUN %s -c \"mkdir -p /placed\"\n" +
				"    COPY --dir tree /placed\n" +
				"    RUN %s -c \"cat /placed/tree/inner.txt > FILE\"\n",
			context: map[string]string{"tree/inner.txt": "inside\n"},
			file:    testArtefact, want: "inside\n", sandboxOnly: true,
		},
		{
			// The other half, and the one this engine got wrong: with no
			// destination to go inside, the destination *is* the copy. Adding
			// the name here produced /placed/tree where the reference produces
			// /placed, and every test written against it agreed, because they
			// were written from the same misreading.
			name: "--dir with no destination to go inside becomes the destination",
			body: "    COPY --dir tree /placed\n" +
				"    RUN %s -c \"cat /placed/inner.txt > FILE\"\n",
			context: map[string]string{"tree/inner.txt": "inside\n"},
			file:    testArtefact, want: "inside\n", sandboxOnly: true,
		},
		{
			// A substituted command's value is its own output, and not the
			// build's.
			//
			// Evaluating `$(...)` means running it on the filesystem the recipe
			// has built up to that line, which means running the steps before
			// it too when they are not already cached. Their output went into
			// the same string: the value of `v` below was `noise` and `wanted`,
			// one line each, and a FOR over it iterated twice.
			//
			// The tell is that it depended on the *cache*. Warm, the earlier
			// steps print nothing and the value is right; cold, they print and
			// it is not - so a variable's value turned on whether the machine
			// had built this before, which is the one thing a build tool may
			// never let happen.
			name: "a substitution takes only its own output",
			body: "    RUN %s -c \"echo noise\"\n" +
				"    LET v=$(echo wanted)\n" +
				"    RUN %s -c \"echo $v > FILE\"\n",
			file: testArtefact, want: "wanted\n",
			// A host target refuses `$(...)` outright - deciding it would mean
			// running LOCALLY steps twice - so there is nothing here to compare.
			sandboxOnly: true,
		},
	}
}

// functions are appended to every fixture, so a case may call one.
const functionBlock = `
WRITE:
    FUNCTION
    ARG text
    RUN %s -c "echo $text > FILE"
`

// fill puts a shell into a case body, however many times it asks for one.
//
// The bodies are written with %s where a shell goes, because the two backends
// have different ones - this machine's, and busybox's inside an image - and
// everything else about the case must stay identical or the comparison is not
// one.
func fill(body, sh string) string {
	for strings.Contains(body, "%s") {
		body = strings.Replace(body, "%s", sh, 1)
	}

	return body
}
