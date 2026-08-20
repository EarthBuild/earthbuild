//go:build darwin

package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/exec"
)

// The two engines are asked the same question and must give the same answer.
//
// This is the differential oracle the test plan is built around, at its
// smallest: the same Earthfile, built by the engine that ships and by this one,
// compared on what came out. Every other test in this repository checks the
// native engine against someone's idea of what it should do - mine, mostly.
// This one checks it against the implementation people are already using, which
// is the only definition of "correct" that a replacement is answerable to.
//
// Artifacts rather than images, because an artifact is bytes and needs no
// normalisation to compare: a difference is a difference. Images carry
// timestamps and digests that legitimately differ, and comparing those needs the
// exclusions table, which is its own piece of work.
func TestBothEnginesProduceTheSameArtifact(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	// Behind its own switch, not just EARTH_TEST_NETWORK.
	//
	// This test drives the engine that ships, which drives a daemon in a
	// container. When that daemon is unhappy the invocation does not fail - it
	// stops making progress, in a way neither a context deadline nor WaitDelay
	// interrupts, and 380 seconds of silence looks like a slow build rather than
	// a stuck one. The rest of the sandbox suite is then held hostage by a
	// dependency that is not even the thing under test.
	//
	// So: opt in with EARTH_TEST_ORACLE=1. The differential is the most valuable
	// test here and the only one that checks this engine against the
	// implementation people use - it is worth running deliberately, and worth
	// not having wedge every other run.
	if os.Getenv("EARTH_TEST_ORACLE") == "" {
		t.Skip("set EARTH_TEST_ORACLE=1 to run the differential against the reference engine")
	}

	earth, err := osexec.LookPath("earth")
	if err != nil {
		t.Skip("the reference engine is not installed")
	}

	err = exec.NewApple().Available()
	if err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	guest := buildGuestd(t)
	cache := storeDir(t)

	// One health check before the table, rather than a deadline per case.
	//
	// When the reference engine is wedged every case waits out its own timeout,
	// so a broken dependency costs five times as long as a working one - and the
	// suite looks like it is doing something. Asking once turns that into a
	// single skip that names what to look at.
	out, err := referenceWorks(t, earth)
	if err != nil {
		t.Skipf("the reference engine is not usable here: %v\n%s"+
			"\n  try restarting its daemon: `docker restart earthly-dev-buildkitd`", err, out)
	}

	for _, tc := range []struct {
		name string
		body string
		// src is a whole Earthfile, for cases the single-target wrapper cannot
		// express. The wrapper covers what one recipe does; the interesting
		// disagreements between two engines are about what one target means to
		// *another*, and those need two targets.
		src string
		// files are written beside the Earthfile, for cases that need more than
		// one - a second Earthfile in a subdirectory, or something to copy.
		files map[string]string
		// diverges says the two engines are *known* to differ here, and why.
		//
		// A divergence that is a decision rather than a defect still belongs in
		// this table: deleting the case would lose the only mechanical record of
		// it, and a note in a document cannot notice when it stops being true.
		// A case with this set fails if the engines ever agree.
		diverges string
	}{
		{
			name: "a command's output",
			body: `    RUN echo differential > /out.txt` + "\n",
		},
		{
			name: "an argument reaching the command",
			body: "    ARG greeting=hello\n" +
				`    RUN echo $greeting > /out.txt` + "\n",
		},
		{
			name: "a condition over an argument",
			body: "    ARG mode=debug\n" +
				"    IF [ \"$mode\" = \"release\" ]\n" +
				`        RUN echo release > /out.txt` + "\n" +
				"    ELSE\n" +
				`        RUN echo debug > /out.txt` + "\n" +
				"    END\n",
		},
		{
			name: "a loop over a list",
			body: "    FOR item IN alpha beta\n" +
				`        RUN echo $item >> /out.txt` + "\n" +
				"    END\n",
		},
		{
			name: "quoting that reaches the shell",
			body: `    RUN echo one two | tr ' ' '-' > /out.txt` + "\n",
		},
		{
			// The semantics chosen from evidence this session, checked against
			// the engine that ships rather than against the tutorials it was
			// inferred from.
			//
			// `SAVE ARTIFACT index.js /dist/index.js` names a file in a
			// namespace of the target's own making, and `COPY +build/dist`
			// names a *directory* in that namespace - which holds index.js and
			// nothing else. Nothing of either name exists in any layer. The
			// rule was read off `examples/tutorial/js/part2`, which is not the
			// same as knowing what the reference does.
			name: "a directory in the artifact namespace",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /js-example
    RUN echo served > index.js
    SAVE ARTIFACT index.js /dist/index.js

probe:
    FROM alpine:3.22
    COPY +build/dist dist
    RUN cat dist/index.js > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// The built-in platform arguments, which this engine did not have
			// at all.
			//
			// They expanded to nothing, silently, which is how `+earthly` in
			// this repository came to write its binary into a directory
			// literally named `$TARGETOS`. Every one of them is a value only
			// the engine knows, so the reference is the only place to get them
			// right - guessing which of USER, NATIVE and TARGET differ on a Mac
			// building for Linux is exactly the kind of reasoning this table
			// exists to replace.
			name: "the built-in platform arguments, undeclared",
			src: `VERSION 0.8

probe:
    FROM alpine:3.22
    RUN echo "target=$TARGETPLATFORM os=$TARGETOS arch=$TARGETARCH var=$TARGETVARIANT" > /out.txt
    RUN echo "user=$USERPLATFORM os=$USEROS arch=$USERARCH var=$USERVARIANT" >> /out.txt
    RUN echo "native=$NATIVEPLATFORM os=$NATIVEOS arch=$NATIVEARCH var=$NATIVEVARIANT" >> /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// And declared, which is the form that carries a value.
			//
			// The case above passes for an engine that has never heard of these
			// names, because undeclared they belong to the shell and the shell
			// has nothing for them either. It was kept precisely for that: it
			// pins the *absence*, and pinning an absence is worth nothing
			// unless something else pins the presence.
			//
			// Twelve values in one artifact so that a single comparison covers
			// the whole table. TARGET and NATIVE agree here and USER does not,
			// which is the distinction an engine treating "the platform" as one
			// fact would get wrong.
			name: "the built-in platform arguments, declared",
			src: `VERSION 0.8

probe:
    FROM alpine:3.22
    ARG TARGETPLATFORM
    ARG TARGETOS
    ARG TARGETARCH
    ARG TARGETVARIANT
    ARG USERPLATFORM
    ARG USEROS
    ARG USERARCH
    ARG USERVARIANT
    ARG NATIVEPLATFORM
    ARG NATIVEOS
    ARG NATIVEARCH
    ARG NATIVEVARIANT
    RUN echo "T $TARGETPLATFORM|$TARGETOS|$TARGETARCH|$TARGETVARIANT" > /out.txt
    RUN echo "U $USERPLATFORM|$USEROS|$USERARCH|$USERVARIANT" >> /out.txt
    RUN echo "N $NATIVEPLATFORM|$NATIVEOS|$NATIVEARCH|$NATIVEVARIANT" >> /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// A destination is a place, and a place can be computed.
			//
			// `SAVE ARTIFACT ... AS LOCAL build/$GOOS/$GOARCH$VARIANT/earthly`
			// is how this repository ships every binary it builds, through two
			// arguments derived from built-ins. With those empty the engine
			// wrote the compiled tool to a directory named `$TARGETOS`, dollar
			// sign and all, and reported success.
			//
			// This case checks the *contents* survive the round trip and that
			// nothing refuses the construct. It does not observe where the file
			// landed - the harness collects out.txt from the project directory
			// and knows nothing of dist/ - which is why the case below exists
			// and why it is written the way it is.
			name: "a local destination computed from a built-in",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    ARG TARGETOS
    ARG TARGETARCH
    ARG GOOS=$TARGETOS
    ARG GOARCH=$TARGETARCH
    RUN echo shipped > /bin.txt
    SAVE ARTIFACT /bin.txt AS LOCAL dist/$GOOS/$GOARCH/bin.txt

probe:
    FROM alpine:3.22
    COPY +build/bin.txt /got.txt
    RUN cat /got.txt > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// A destination that observes itself.
			//
			// The harness collects `out.txt` from the project directory, so the
			// filename *is* the assertion: with `$VARIANT` undeclared the
			// reference writes out.txt and this engine wrote `out$VARIANT.txt`,
			// which the harness then reports as missing. No comparison of
			// contents can catch a file in the wrong place; a case whose
			// correctness decides its own name can.
			//
			// The shape is this repository's own - `AS LOCAL
			// "build/$GOARCH$VARIANT/earthly"` - reduced until the only thing
			// left is the question.
			name: "an undeclared name in a local destination",
			src: `VERSION 0.8

probe:
    FROM alpine:3.22
    RUN echo shipped > /shipped.txt
    SAVE ARTIFACT /shipped.txt AS LOCAL out$VARIANT.txt
`,
		},
		{
			// Where a *directory* artifact lands, which the rules above do not
			// settle.
			//
			// `COPY --dir +code/earthly /` is the repository's own Earthfile,
			// and `+lint` two lines later looks for go.mod at the working
			// directory - so the two readings are not academic: one leaves the
			// tree at /earthly and the other spreads it across /. E32 settled
			// that `--dir` itself means nothing to an artifact, which leaves the
			// question of the destination unanswered rather than answered.
			//
			// Both spellings are here because the rule is presumably `cp`'s -
			// a destination that already names a directory takes the source
			// *inside* it - and a case that only tried one would confirm
			// whichever half this engine happens to implement.
			name: "a directory artifact copied into a destination that exists",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    RUN mkdir -p /bundle/nested && echo top > /bundle/top.txt && echo in > /bundle/nested/inner.txt
    SAVE ARTIFACT /bundle

probe:
    FROM alpine:3.22
    RUN mkdir -p /there
    COPY +build/bundle /there/
    COPY +build/bundle /fresh
    RUN ls /there > /out.txt && ls /fresh >> /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// The whole matrix at once, because the two cases either side of
			// this one disagree about which fact decides.
			//
			// `--dir` and the existence of the destination are the two
			// candidates, and each has evidence: E32 concluded `--dir` means
			// nothing to an artifact, from a destination that did not exist,
			// and the root case below shows the reference keeping the directory
			// name where this engine does not. A case per combination is the
			// only way to tell a rule from a coincidence.
			name: "every combination of --dir and an existing destination",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    RUN mkdir -p /bundle/nested && echo top > /bundle/top.txt && echo in > /bundle/nested/inner.txt
    SAVE ARTIFACT /bundle

probe:
    FROM alpine:3.22
    RUN mkdir -p /d-exists /nd-exists
    COPY --dir +build/bundle /d-exists
    COPY --dir +build/bundle /d-missing
    COPY +build/bundle /nd-exists
    COPY +build/bundle /nd-missing
    RUN echo "d-exists:" > /out.txt && ls /d-exists >> /out.txt
    RUN echo "d-missing:" >> /out.txt && ls /d-missing >> /out.txt
    RUN echo "nd-exists:" >> /out.txt && ls /nd-exists >> /out.txt
    RUN echo "nd-missing:" >> /out.txt && ls /nd-missing >> /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// And the same matrix for a source in the build context, which is
			// the other half of the rule and the half already implemented.
			//
			// Asked in the same session as the artifact matrix on purpose: a
			// fix that teaches one of them about the destination and leaves the
			// other alone is how the last three defects here were made.
			name: "every combination of --dir for a context source",
			files: map[string]string{
				"tree/top.txt":          "top\n",
				"tree/nested/inner.txt": "in\n",
			},
			src: `VERSION 0.8

probe:
    FROM alpine:3.22
    RUN mkdir -p /d-exists /nd-exists
    COPY --dir tree /d-exists
    COPY --dir tree /d-missing
    COPY tree /nd-exists
    COPY tree /nd-missing
    RUN echo "d-exists:" > /out.txt && ls /d-exists >> /out.txt
    RUN echo "d-missing:" >> /out.txt && ls /d-missing >> /out.txt
    RUN echo "nd-exists:" >> /out.txt && ls /nd-exists >> /out.txt
    RUN echo "nd-missing:" >> /out.txt && ls /nd-missing >> /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// The same question at the root, which is the form the repository's
			// own Earthfile uses. `/` always exists, so if a destination that
			// exists takes the directory inside it, this leaves /bundle.
			name: "a directory artifact copied to the root",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    RUN mkdir -p /bundle && echo top > /bundle/top.txt
    SAVE ARTIFACT /bundle

probe:
    FROM alpine:3.22
    COPY --dir +build/bundle /
    RUN ls / > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// COPY's destination resolves against WORKDIR, and `.` means the
			// working directory rather than the filesystem root. Written from
			// reasoning about what the command must mean, which is the same
			// footing as the namespace rules above and deserves the same check.
			name: "a destination resolved against WORKDIR",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /work
    RUN echo alpha > a.txt
    SAVE ARTIFACT a.txt

probe:
    FROM alpine:3.22
    WORKDIR /dest
    COPY +build/a.txt .
    RUN cat /dest/a.txt > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// `+target/*` is every artifact that target saved, expanded here
			// rather than passed to the guest as a path called `*`. Each lands
			// under the name it was given.
			name: "a glob over everything a target saved",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /work
    RUN echo one > one.txt
    RUN echo two > two.txt
    SAVE ARTIFACT one.txt
    SAVE ARTIFACT two.txt

probe:
    FROM alpine:3.22
    COPY +build/* /got/
    RUN cat /got/one.txt /got/two.txt > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// Where a copied directory's contents actually land, rather than
			// where I assumed they would.
			//
			// The first version of this case asserted `/here/sub/b.txt` and the
			// reference could not build it at all - `--dir` on an *artifact*
			// reference does not wrap the tree in its own name the way it does
			// for a build context. A differential whose case encodes a guess
			// tests the guess; `find` asks both engines where they put it and
			// compares the answers.
			name: "where a copied directory's contents land",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /work
    RUN mkdir -p sub && echo beta > sub/b.txt
    SAVE ARTIFACT sub

probe:
    FROM alpine:3.22
    COPY --dir +build/sub /here
    RUN find /here | sort > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// `FROM +target` inherits the target's *state*, not just its
			// filesystem: the working directory it left, and the environment it
			// set. Both are easy to rebuild as a filesystem and lose.
			name: "what FROM +target inherits",
			src: `VERSION 0.8

common:
    FROM alpine:3.22
    WORKDIR /w
    ENV COLOUR=green

probe:
    FROM +common
    RUN echo $COLOUR > out.txt
    RUN pwd >> out.txt
    SAVE ARTIFACT out.txt AS LOCAL out.txt
`,
		},
		{
			// A trailing slash on the destination is not decoration: it says
			// "into this directory" where its absence says "as this name". The
			// rule was written from reasoning about what COPY must mean, so it
			// is on the same footing as the artifact rules that turned up a bug.
			name: "a destination with and without a trailing slash",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /w
    RUN echo x > f.txt
    SAVE ARTIFACT f.txt

probe:
    FROM alpine:3.22
    RUN mkdir -p /d
    COPY +build/f.txt /d/
    COPY +build/f.txt /e
    RUN find /d /e | sort > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// An argument supplied where the artifact is *referenced*, which
			// makes the producing target a different build.
			name: "a build argument on a copy reference",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    ARG flavour=plain
    RUN echo $flavour > /f.txt
    SAVE ARTIFACT /f.txt

probe:
    FROM alpine:3.22
    COPY (+build/f.txt --flavour=spicy) /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// A condition the interpreter cannot decide without running the
			// steps before it. Everything else in this table can be answered
			// from the text; this one needs a filesystem that does not exist
			// until the build is under way.
			name: "a condition over a file an earlier step wrote",
			body: `    RUN echo marker > /flag` + "\n" +
				"    IF [ -f /flag ]\n" +
				`        RUN echo present > /out.txt` + "\n" +
				"    ELSE\n" +
				`        RUN echo absent > /out.txt` + "\n" +
				"    END\n",
		},
		{
			// ARG and ENV under one name. Both put a value in the environment a
			// command sees, by different routes and with different lifetimes,
			// and which one a RUN reads is a rule rather than an accident.
			name: "an ARG and an ENV of the same name",
			body: "    ARG label=from-arg\n" +
				"    ENV label=from-env\n" +
				`    RUN echo $label > /out.txt` + "\n",
		},
		{
			// What a CACHE holds must not be in the image. The contents live in
			// a mount that outlives the step, so a target building on this one
			// sees the directory and not what was written into it - and a build
			// that put the cache in the layer would produce an image whose
			// contents depend on what happened to be cached on that machine.
			name: "what a CACHE leaves behind in the image",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    CACHE /cache
    RUN echo cached > /cache/f.txt

probe:
    FROM +build
    RUN ls -A /cache > /out.txt 2>&1 || true
    RUN echo end >> /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// The other half of the rule, and the guard on the fix for the case
			// above. A mount point the *image* already had is not this engine's
			// to take away: what was under the hole stays exactly as it was, so
			// the original file survives and the cached one does not.
			//
			// Without this case, "remove the mount point afterwards" passes its
			// own test by deleting a directory that belonged to the image.
			name: "a CACHE over a directory the image already had",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    RUN mkdir -p /pre && echo original > /pre/keep.txt
    CACHE /pre
    RUN echo cached > /pre/f.txt

probe:
    FROM +build
    RUN ls -A /pre > /out.txt 2>&1 || echo MISSING > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// A file's mode and a symlink's target, carried through an artifact.
			//
			// Both are properties of the file rather than of its contents, and
			// both are easy to lose in a copy that reads bytes and writes them
			// somewhere else - which is what a naive implementation of COPY is.
			name: "a mode and a symlink through an artifact",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /w
    RUN echo x > f.txt && chmod 741 f.txt && ln -s f.txt link.txt
    SAVE ARTIFACT f.txt
    SAVE ARTIFACT link.txt

probe:
    FROM alpine:3.22
    COPY +build/f.txt /got-f
    COPY +build/link.txt /got-link
    RUN stat -c '%a %F' /got-f > /out.txt
    RUN readlink /got-link >> /out.txt || echo "not a link" >> /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// The mtime, which green paper I8 says is part of a layer's
			// identity. A build tool that stamps outputs with the current time
			// defeats every downstream tool that compares timestamps, and this
			// engine goes to some trouble to preserve them - trouble that is
			// only worth anything if the answer matches what people already get.
			name: "an mtime carried through an artifact",
			diverges: "the reference clamps mtimes to a fixed epoch for reproducibility; " +
				"this engine preserves them because I8 makes them part of a layer's identity",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /w
    RUN echo x > f.txt && touch -d '2001-02-03 04:05:06' f.txt
    SAVE ARTIFACT f.txt

probe:
    FROM alpine:3.22
    COPY +build/f.txt /got.txt
    RUN stat -c '%y' /got.txt > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// WORKDIR names a directory that need not exist yet.
			name: "a WORKDIR that does not exist",
			body: "    WORKDIR /brand/new/dir\n" +
				`    RUN pwd > /out.txt` + "\n" +
				`    RUN ls -d /brand/new/dir >> /out.txt` + "\n",
		},
		{
			// The same mtime question with the flag that asks for it. The
			// reference preserves when told to, and so does this engine -
			// which preserves either way - so the two agree *here* and differ
			// only on the default. That is the whole shape of E34 in one case.
			name: "an mtime carried through an artifact with --keep-ts",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /w
    RUN echo x > f.txt && touch -d '2001-02-03 04:05:06' f.txt
    SAVE ARTIFACT --keep-ts f.txt

probe:
    FROM alpine:3.22
    COPY --keep-ts +build/f.txt /got.txt
    RUN stat -c '%y' /got.txt > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// A symlink to a directory, saved and copied. Asked because the
			// answer could not be reasoned out: Docker carries a build
			// context's symlinks as links, and an artifact is not a build
			// context - it is one target's output arriving in another.
			//
			// The reference dereferences, and this case is what established
			// that. This engine copied the link, so what arrived in the image
			// was a link naming a path that image does not have; the same three
			// lines also followed an *absolute* link onto the guest's own
			// filesystem, which is a step reaching outside itself (A3).
			name: "a symlink to a directory through an artifact",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /w
    RUN mkdir -p real && echo inside > real/a.txt && ln -s real link
    SAVE ARTIFACT link

probe:
    FROM alpine:3.22
    COPY +build/link got
    RUN { readlink got || echo "(not a link)"; cat got/a.txt; } > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// The same symlink, with the flag that asks for the opposite.
			//
			// E75 measured which side of the copy carries the meaning by varying
			// it one side at a time; this pins the answer now that the engine
			// implements it. The link arrives as a link and dangles, because
			// `real` was not copied - which is what the author asked for and
			// what the reference does.
			//
			// `|| true` on the cat, because a dangling link is the expected
			// result and a case that failed the build would be measuring the
			// shell rather than the engines.
			name: "a symlink to a directory with --symlink-no-follow",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /w
    RUN mkdir -p real && echo inside > real/a.txt && ln -s real link
    SAVE ARTIFACT --symlink-no-follow link
    SAVE ARTIFACT real

probe:
    FROM alpine:3.22
    COPY --symlink-no-follow +build/link got
    RUN { readlink got || echo "(not a link)"; cat got/a.txt 2>&1 || true; } > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// File ownership through an artifact. Asked because `--keep-own`
			// sits in this engine's refusal list, and `--keep-ts` turned out to
			// be asking for behaviour that was already the default - so the
			// list is worth checking flag by flag rather than trusted (E34).
			name: "ownership through an artifact",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /w
    RUN echo x > f.txt && chown 65534:65534 f.txt
    SAVE ARTIFACT f.txt

probe:
    FROM alpine:3.22
    COPY +build/f.txt /got.txt
    RUN stat -c '%u %g' /got.txt > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// The same question with the flag that asks for it, which used to be
			// the answer to why the row above is a gap rather than a divergence:
			// the defaults agreed and `--keep-own` was the only thing missing
			// (E34). Now implemented, so both engines must deliver 65534.
			//
			// A directory as well as a file, because a tree whose root kept its
			// ownership and whose contents reverted would pass a file-only case
			// and produce something that looks like corruption in an image.
			name: "ownership through an artifact with --keep-own",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /w
    RUN mkdir -p d && echo x > d/f.txt && chown -R 65534:65534 d
    SAVE ARTIFACT --keep-own d

probe:
    FROM alpine:3.22
    COPY --keep-own --dir +build/d /d
    RUN stat -c '%u %g' /d > /out.txt
    RUN stat -c '%u %g' /d/f.txt >> /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
			diverges: "on a macOS host this engine cannot deliver ownership at all," +
				" and refuses rather than delivering the wrong owner." +
				" The layer store is a host directory shared into the sandbox (E1b)," +
				" and macOS maps everything written through that share to the user" +
				" running the build: measured, a file the step made 65534:65534 is" +
				" 501:20 in the store. The reference's store lives inside its" +
				" daemon's Linux volume and never touches the host filesystem." +
				" Green paper A2 covers this and requires saying so rather than" +
				" degrading, so the build fails with the reason. On a Linux host" +
				" the store has real uids and the engines agree - at which point" +
				" this case fails and the note is removed.",
		},
		{
			// A user-defined function and a call with an argument. FUNCTION and
			// DO are a whole second scoping rule - a function's ARG is not the
			// caller's - and nothing else in this table exercises it.
			name: "a function called with an argument",
			src: `VERSION 0.8

GREET:
    FUNCTION
    ARG name=world
    RUN echo hello $name > /out.txt

probe:
    FROM alpine:3.22
    DO +GREET --name=earth
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// An argument expanded inside a path, on both sides of an artifact:
			// where it is saved and where it lands. Expansion is the
			// interpreter's, not the shell's, so a RUN cannot stand in for it.
			name: "an argument expanded inside a path",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    ARG dir=out
    RUN mkdir -p /$dir && echo v > /$dir/f.txt
    SAVE ARTIFACT /$dir/f.txt

probe:
    FROM alpine:3.22
    ARG dest=/placed
    COPY +build/f.txt $dest
    RUN cat $dest > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// A second FROM part-way through a recipe. It replaces what the
			// target was built on, so everything written before it goes - which
			// is easy to implement as "carry on from here" and be wrong about.
			name: "a second FROM part-way through",
			body: `    RUN echo first > /a.txt` + "\n" +
				"    FROM alpine:3.22\n" +
				`    RUN (cat /a.txt || echo gone) > /out.txt` + "\n",
		},
		{
			// A target in another Earthfile. Cross-file references are how any
			// project larger than a tutorial is arranged, and they bring their
			// own rules: the referenced file's directory is *its* build
			// context, not the caller's.
			name: "a target in another Earthfile",
			src: `VERSION 0.8

probe:
    FROM alpine:3.22
    COPY ./lib+make/f.txt /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
			files: map[string]string{
				"lib/Earthfile": `VERSION 0.8

make:
    FROM alpine:3.22
    COPY seed.txt /seed.txt
    RUN cat /seed.txt > /f.txt
    SAVE ARTIFACT /f.txt
`,
				// Beside lib/Earthfile, so a copy that resolved against the
				// caller's directory instead would not find it.
				"lib/seed.txt": "from-the-library\n",
			},
		},
		{
			// A WAIT block, which says what must have finished before what
			// follows it. Nothing else in this table constrains ordering.
			name: "a WAIT block",
			body: "    WAIT\n" +
				`        RUN echo first > /out.txt` + "\n" +
				"    END\n" +
				`    RUN echo second >> /out.txt` + "\n",
		},
		{
			// LOCALLY, which runs on the machine rather than in a sandbox.
			//
			// Worth a differential precisely because it is the construct with
			// no sandbox between the Earthfile and the developer's disk: if the
			// two engines disagree about what it means, they disagree about
			// what someone's machine is about to do.
			//
			// No SAVE ARTIFACT: a LOCALLY target's output is already local,
			// which is the whole of what the command says.
			name: "a command that runs on this machine",
			src: `VERSION 0.8

probe:
    LOCALLY
    RUN echo local-run > out.txt
`,
		},
		{
			// The image's *configuration*, read back out of docker.
			//
			// This is the blind spot the vocabulary table cannot cover and the
			// rest of this one does not reach: every other case observes a
			// filesystem, and ENTRYPOINT, CMD, USER, ENV, LABEL, EXPOSE and
			// VOLUME are not in the filesystem. `VOLUME` was accepted and
			// silently dropped from the image for as long as anyone can tell
			// (E39), and nothing here would have noticed.
			//
			// Read field by field rather than as one JSON blob: the two engines
			// run different docker versions inside their sandboxes, and
			// comparing whole `inspect` output would compare those instead.
			//
			// The label is read by name for a related reason: the reference
			// stamps `dev.earthly.*` provenance labels of its own, which is its
			// business and not a disagreement about what the Earthfile said.
			name: "the configuration of a saved image",
			src: `VERSION 0.8

app:
    FROM alpine:3.22
    WORKDIR /w
    ENV K=V
    USER nobody
    EXPOSE 8080
    VOLUME /data
    LABEL role=probe
    ENTRYPOINT ["/bin/echo", "hi"]
    CMD ["there"]
    SAVE IMAGE cfg-probe:latest

probe:
    FROM alpine:3.22
    WITH DOCKER --load cfg-probe:latest=+app
        RUN docker inspect -f '{{.Config.User}}|{{.Config.WorkingDir}}|{{json .Config.Entrypoint}}|{{json .Config.Cmd}}|{{index .Config.Labels "role"}}|{{json .Config.ExposedPorts}}|{{json .Config.Volumes}}' cfg-probe:latest > /out.txt
    END
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
		{
			// The sibling rule: an artifact named in full, rather than by the
			// directory holding it.
			name: "an artifact named the way its author named it",
			src: `VERSION 0.8

build:
    FROM alpine:3.22
    WORKDIR /js-example
    RUN echo served > index.js
    SAVE ARTIFACT index.js /dist/index.js

probe:
    FROM alpine:3.22
    COPY +build/dist/index.js app.js
    RUN cat app.js > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			if src == "" {
				src = "VERSION 0.8\n\nprobe:\n    FROM alpine:3.22\n" + tc.body +
					"    SAVE ARTIFACT /out.txt AS LOCAL out.txt\n"
			}

			dir := project(t, src, tc.files)
			out := filepath.Join(dir, testArtefact)

			// The engine that ships, first - under a deadline.
			//
			// Without one a wedged reference engine hangs the whole suite
			// rather than failing this test, which is what happened: a daemon
			// restart left it unable to make progress, and 520 seconds of
			// silence looked like a slow build instead of a stuck one. An
			// external tool gets a bounded wait or it gets to decide how long
			// the tests take.
			refCtx, cancelRef := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancelRef()

			// Output to a *file*, not a pipe, and this is what actually bounds
			// it.
			//
			// `CombinedOutput` and `Output` read through pipes, and Wait does not
			// return until those pipes reach EOF. The reference engine leaves
			// child processes holding its stdout - it talks to a daemon through
			// helpers - so killing it on a deadline closed nothing and the read
			// blocked forever. Neither the context nor WaitDelay helped, because
			// the process being waited for was not the one holding the pipe.
			//
			// A file has no such problem: the fd is passed straight to the child,
			// there is no copying goroutine, and Wait returns when the process
			// this test started is gone.
			refLog, err := os.CreateTemp(t.TempDir(), "reference-*.log")
			if err != nil {
				t.Fatal(err)
			}

			// -P because the reference refuses WITH DOCKER without it:
			// `security.insecure is not allowed`. It runs its daemon by asking
			// buildkit for a privileged container, where this engine boots a VM
			// that already has one - so the flag is a difference in how the two
			// get a daemon, not in what the Earthfile asked for. Inert for every
			// case that does not use WITH DOCKER.
			ref := osexec.CommandContext(refCtx, earth, "--allow-privileged", "--no-cache", "+probe")
			ref.Dir = dir
			ref.Stdout = refLog
			ref.Stderr = refLog

			// WaitDelay as well as the context, and this is the part that
			// actually bounds it. Killing the process does not close the pipes
			// its *children* inherited - the reference engine talks to a daemon
			// through helpers - so CombinedOutput went on waiting for output
			// from processes that outlived the one that was killed. The deadline
			// fired and the test hung anyway.
			ref.WaitDelay = 5 * time.Second

			runErr := ref.Run()

			_ = refLog.Close()

			b, _ := os.ReadFile(refLog.Name())

			if errors.Is(refCtx.Err(), context.DeadlineExceeded) {
				t.Skipf("the reference engine did not finish in 90s; is buildkitd healthy?\n%s", b)
			}

			if runErr != nil {
				if strings.Contains(string(b), "429") {
					t.Skipf("docker hub rate limit: %s", b)
				}

				t.Fatalf("the reference engine could not build this: %v\n%s", runErr, b)
			}

			want, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("the reference engine produced no artifact: %v", err)
			}

			err = os.Remove(out)
			if err != nil {
				t.Fatal(err)
			}

			// Then this one, asked exactly the same thing.
			t.Setenv("EARTH_GUESTD", guest)
			useStore(t, cache)

			var log bytes.Buffer

			err = cli.Run(context.Background(), cli.Options{
				Dir: dir, Target: testProbe, Out: &log, Platform: "linux/arm64",
			})
			if err != nil {
				if strings.Contains(err.Error(), "429") {
					t.Skipf("docker hub rate limit: %v", err)
				}

				// A declared divergence where this engine *refuses* is still a
				// divergence, and the harness could only express "different
				// bytes" - so a case whose whole point was an honest refusal
				// failed here as though the refusal were the fault.
				//
				// It is the more interesting shape of the two: the reference
				// produces something and this engine says it cannot, which is
				// I10 working. Recorded rather than tolerated, and it stops
				// being recorded the moment the refusal goes.
				if tc.diverges != "" {
					t.Logf("known divergence (%s):\n  reference: %q\n  native refused: %v",
						tc.diverges, want, err)

					return
				}

				t.Fatalf("%v\n%s", err, log.String())
			}

			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("this engine produced no artifact: %v\n%s", err, log.String())
			}

			// A case may be a *known* divergence rather than a check for
			// agreement. Recorded this way rather than deleted, because a note
			// in a document cannot notice when it stops being true: if the two
			// ever agree here, this fails and says the note is stale.
			if tc.diverges != "" {
				if bytes.Equal(got, want) {
					t.Errorf("the engines now agree, so this is no longer a divergence: %s"+
						"\n  both: %q\n  remove the `diverges` note and make it an ordinary case",
						tc.diverges, got)
				}

				t.Logf("known divergence (%s):\n  reference: %q\n  native:    %q", tc.diverges, want, got)

				return
			}

			if !bytes.Equal(got, want) {
				t.Errorf("the engines disagree:\n  reference: %q\n  native:    %q", want, got)
			}
		})
	}
}

// referenceWorks builds a trivial target to see whether the reference engine can
// make progress at all.
//
// Deliberately a *build* rather than `--version`: the failure being detected is
// a daemon that has stopped responding, and a version string is printed without
// ever talking to it.
func referenceWorks(t *testing.T, earth string) (string, error) {
	t.Helper()

	dir := project(t, "VERSION 0.8\n\nprobe:\n    FROM alpine:3.22\n    RUN true\n", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	log, err := os.CreateTemp(t.TempDir(), "health-*.log")
	if err != nil {
		return "", err
	}

	cmd := osexec.CommandContext(ctx, earth, "--no-output", "+probe")
	cmd.Dir = dir
	cmd.Stdout = log
	cmd.Stderr = log

	runErr := cmd.Run()

	_ = log.Close()

	b, _ := os.ReadFile(log.Name())

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return string(b), errors.New("it did not finish a trivial build in 90s")
	}

	return string(b), runErr
}
