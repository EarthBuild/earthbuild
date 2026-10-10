package interp_test

import (
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// Every command in the language, and whether this engine takes it.
//
// The table exists because a claim about what an engine *cannot* do has no
// executable consequence, so it survives the moment it stops being true.
// Twice in one week: a note said a target called `base` was accepted here and
// the parser had always refused it (E36), and the plan said LOCALLY was refused
// when it planned, ran, and matched the reference (E42). Both were believed for
// as long as they went unchecked, and a reader planning work would have acted
// on either.
//
// So the claims are written down here instead of in prose, and the suite
// disagrees with them when they go stale - in *both* directions. A construct
// that starts working fails this test just as loudly as one that stops.
func TestTheVocabularyIsWhatWeSayItIs(t *testing.T) {
	t.Parallel()

	// A minimal use of each command, in a target unless it must be elsewhere.
	// `supported` is the claim being made about this engine, not about the
	// language.
	for _, tc := range []struct {
		cmd       string
		body      string
		supported bool
	}{
		{cmd: "ARG", body: "    ARG x=1\n", supported: true},
		{cmd: "BUILD", body: "    BUILD +other\n", supported: true},
		{cmd: "CACHE", body: "    CACHE /c\n", supported: true},
		{cmd: "CMD", body: `    CMD ["/bin/sh"]` + "\n", supported: true},
		{cmd: testCmdCopy, body: "    COPY +other/f.txt .\n", supported: true},
		{cmd: "ENTRYPOINT", body: `    ENTRYPOINT ["/bin/sh"]` + "\n", supported: true},
		{cmd: "ENV", body: "    ENV k=v\n", supported: true},
		{cmd: "EXPOSE", body: "    EXPOSE 8080\n", supported: true},
		{cmd: "FOR", body: "    FOR x IN a b\n        RUN echo $x\n    END\n", supported: true},
		// Supported. The first draft of this table said otherwise, on the
		// strength of an `unsupported("GIT CLONE --keep-ts")` call site - which
		// refuses a *flag*, not the command. The guard caught it on its first
		// run, which is the third stale claim about absence in a week.
		{cmd: "GIT CLONE", body: "    GIT CLONE https://example.test/r.git /r\n", supported: true},
		{cmd: "HEALTHCHECK", body: "    HEALTHCHECK CMD true\n", supported: true},
		// Implemented on 2026-08-19 (E415): entries reach the step as an
		// /etc/hosts bound in, and the step resolves by them.
		{cmd: "HOST", body: "    HOST example.test 1.2.3.4\n", supported: true},
		{cmd: "IF", body: "    IF [ \"a\" = \"a\" ]\n        RUN echo hi\n    END\n", supported: true},
		{cmd: "LABEL", body: "    LABEL a=b\n", supported: true},
		{cmd: testCmdLet, body: "    LET x = 1\n", supported: true},
		{cmd: "LOCALLY", body: "    LOCALLY\n", supported: true},
		{cmd: "RUN", body: "    RUN make\n", supported: true},
		{cmd: testCmdSaveArtifact, body: "    RUN make\n    SAVE ARTIFACT /out\n", supported: true},
		{cmd: testCmdSaveImage, body: "    SAVE IMAGE thing:latest\n", supported: true},
		{cmd: "SHELL", body: `    SHELL ["/bin/sh", "-c"]` + "\n", supported: false},
		{cmd: "STOPSIGNAL", body: "    STOPSIGNAL SIGTERM\n", supported: true},
		// **Accepted, and only half honoured** - which this column cannot say,
		// so the comment must. `USER` reaches the image *configuration*, so a
		// container started from the image runs as that user; it does not
		// reach a *step*, and `RUN id -un` after `USER testuser` prints
		// `root`. `ir.Op.User` is carried and keyed and consumed nowhere.
		//
		// Left as supported because the measure here is refusal, and the
		// command is not refused. E719 has the measurement and the reason it
		// was not fixed in passing: dropping privileges carelessly is worse
		// than not dropping them, and an Earthfile that says `USER nobody` and
		// gets root should be fixed deliberately or refused by name.
		{cmd: "USER", body: "    USER nobody\n", supported: true},
		{cmd: "VOLUME", body: "    VOLUME /data\n", supported: true},
		{cmd: "WAIT", body: "    WAIT\n        RUN make\n    END\n", supported: true},
		{cmd: "WORKDIR", body: "    WORKDIR /w\n", supported: true},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+`
other:
    FROM alpine:3.22
    RUN make
    SAVE ARTIFACT f.txt

probe:
    FROM alpine:3.22
`+tc.body, "probe")

			// Only a refusal *by name* counts as unsupported. Everything else -
			// a missing file, a target that saves nothing - is this table's own
			// fixture being wrong, and saying so beats recording it as a gap.
			// errors.Is, not a phrase. This read "not supported by the native
			// engine", which is one of three refusal wordings - a construct
			// the language lacks and a construct refused on purpose say
			// something else - so adding either made a still-refused flag look
			// supported. E151's lesson, arriving from the other side: classify
			// with a type, do not read the message.
			refused := errors.Is(err, interp.ErrRefused)

			switch {
			case tc.supported && refused:
				t.Errorf("%s is refused, and this table says it is supported:\n%v", tc.cmd, err)
			case !tc.supported && !refused:
				t.Errorf("%s is no longer refused - the claim that it is has gone stale."+
					"\n  mark it supported here, and check what else says otherwise", tc.cmd)
			case tc.supported && err != nil:
				// Accepted, and something else went wrong: the fixture, not the
				// engine. Reported rather than swallowed, because a fixture
				// that stops exercising the command tests nothing.
				t.Logf("%s: accepted, and the fixture errored: %v", tc.cmd, err)
			}
		})
	}
}

// The same claim, one level down: which flags this engine takes.
//
// The command table above exists because `LOCALLY` was refused in the notes and
// not in the engine. The flag table exists because the opposite happened:
// `--keep-ts` was refused by the engine while this engine already did exactly
// what it asks, so an Earthfile was turned away for requesting the behaviour it
// was about to get (E34). That was found by reading the refusal list by hand,
// which is not a thing anyone does twice.
//
// Same rule as above, and the same two directions: a flag that starts working
// fails this as loudly as one that stops.
func TestTheFlagsAreWhatWeSayTheyAre(t *testing.T) {
	t.Parallel()

	ctx := ctxWith(t, map[string]string{
		testSourceFile:   "x\n",
		"tree/f.txt":     "y\n",
		testLibEarthfile: versioned + "\nthing:\n    FROM alpine:3.22\n    RUN make\n    SAVE ARTIFACT /out\n",
	})

	for _, tc := range []struct {
		flag      string
		body      string
		supported bool
	}{
		// COPY. --keep-ts is supported and was not: it asks for what this
		// engine does unconditionally.
		{flag: "COPY --dir", body: "    COPY --dir tree /t\n", supported: true},
		{flag: "COPY --if-exists", body: "    COPY --if-exists src.txt /s\n", supported: true},
		{flag: "COPY --keep-ts", body: "    COPY --keep-ts src.txt /s\n", supported: true},
		{flag: "COPY --chmod", body: "    COPY --chmod=0755 src.txt /s\n", supported: true},
		// Implemented on 2026-08-19 (E419): the names resolve against the
		// destination image, in the guest, because only it has that image.
		{flag: "COPY --chown", body: "    COPY --chown=1:1 src.txt /s\n", supported: true},
		{flag: "COPY --keep-own", body: "    COPY --keep-own src.txt /s\n", supported: true},
		// Implemented, once measurement established which side of the copy
		// carries its meaning (E75, E83).
		{flag: "COPY --symlink-no-follow", body: "    COPY --symlink-no-follow src.txt /s\n", supported: true},

		// SAVE ARTIFACT.
		{flag: "SAVE ARTIFACT --if-exists", body: "    RUN make\n    SAVE ARTIFACT --if-exists /out\n", supported: true},
		{flag: "SAVE ARTIFACT --keep-ts", body: "    RUN make\n    SAVE ARTIFACT --keep-ts /out\n", supported: true},
		{flag: "SAVE ARTIFACT --keep-own", body: "    RUN make\n    SAVE ARTIFACT --keep-own /out\n", supported: true},
		{flag: testForcedArtifact, body: "    RUN make\n    SAVE ARTIFACT --force /out\n", supported: true},

		// RUN.
		{flag: "RUN --no-cache", body: "    RUN --no-cache make\n", supported: true},
		{flag: "RUN --entrypoint", body: "    RUN --entrypoint -- -f x\n", supported: true},
		{flag: "RUN --mount type=cache", body: "    RUN --mount=type=cache,target=/c make\n", supported: true},
		{flag: "RUN --mount type=tmpfs", body: "    RUN --mount=type=tmpfs,target=/t make\n", supported: true},
		// The fields of a mount, which were read into a map and dropped (E435).
		{flag: "RUN --mount sharing", body: "    RUN --mount=type=cache,target=/c,sharing=locked make\n", supported: true},
		{flag: "RUN --mount mode", body: "    RUN --mount=type=cache,target=/c,mode=0700 make\n", supported: true},
		{flag: "RUN --mount chmod", body: "    RUN --mount=type=cache,target=/c,chmod=0700 make\n", supported: true},
		{flag: "RUN --mount ro", body: "    RUN --mount=type=cache,target=/c,ro make\n", supported: true},
		{flag: "RUN --mount uid", body: "    RUN --mount=type=cache,target=/c,uid=1000 make\n", supported: false},
		{flag: "RUN --mount gid", body: "    RUN --mount=type=cache,target=/c,gid=1000 make\n", supported: false},
		{flag: "RUN --mount from", body: "    RUN --mount=type=cache,target=/c,from=+b make\n", supported: false},

		// CACHE.
		{flag: "CACHE --id", body: "    CACHE --id=x /c\n", supported: true},
		{flag: "CACHE --sharing", body: "    CACHE --sharing=shared /c\n", supported: true},
		// The three modes are `locked`, `shared` and `private` (E432); a fourth
		// name is a dialect this engine does not have, and is still refused.
		{flag: "CACHE --sharing=other", body: "    CACHE --sharing=none /c\n", supported: false},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+`
probe:
    FROM alpine:3.22
`+tc.body, "probe", interp.WithContext(ctx))

			// The second of the two places this question is asked in this file.
			// Changing only the first left this one matching a phrase, and it
			// reported a still-refused flag as supported - the same "applied at
			// one of the two places it holds" shape the engine has been bitten
			// by twice, here inside the test that catches it.
			refused := errors.Is(err, interp.ErrRefused)

			switch {
			case tc.supported && refused:
				t.Errorf("%s is refused, and this table says it is supported:\n%v", tc.flag, err)
			case !tc.supported && !refused:
				t.Errorf("%s is no longer refused - the claim that it is has gone stale."+
					"\n  mark it supported here, and check what else says otherwise", tc.flag)
			case tc.supported && err != nil:
				t.Logf("%s: accepted, and the fixture errored: %v", tc.flag, err)
			}
		})
	}
}
