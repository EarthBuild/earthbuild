package interp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A refusal does not offer a way out that does not work.
//
// Every `unsupported` refusal ends `to build this now, use --engine=buildkit`,
// which is right for a native-engine gap and wrong for `COPY --from`:
// docs/earthfile/earthfile.md says of it *"Although this option is present in
// classical Dockerfile syntax, it is not supported by Earthfiles"*. The other
// engine refuses it identically, so the reader is sent to repeat the build with
// a different flag and get the same answer.
//
// **That is worse than offering nothing.** A refusal with no remedy costs a
// search; a refusal with a false remedy costs a build, and it is believed on the
// way because the engine said it. I10 is honest refusal, and a remedy is part of
// what is being asserted.
//
// The E68 shape, in its expensive direction - `--keep-ts` was refused while this
// engine did exactly what it asks. Here the refusal is right and only the way
// out is wrong.
//
// The flag *is* implemented for Dockerfile syntax, where the language has it
// (see dockerfile_test.go), so the message can say where it works rather than
// only where it does not.
func TestARefusalForSomethingTheLanguageLacksOffersNoEngineSwitch(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    COPY --from=other /a /b\n", testMain)
	if err == nil {
		t.Fatal("COPY --from was accepted in Earthfile syntax")
	}

	if strings.Contains(err.Error(), "--engine=buildkit") {
		t.Errorf("the refusal offers an engine that refuses this too:\n%s", err)
	}

	// What it must say instead. Not a wording check - these are the two names a
	// reader needs to find the thing that does work, and the documentation
	// gives exactly this pair as the replacement.
	for _, want := range []string{testCmdSaveArtifact, testCmdCopy} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so it says only what does not"+
				" work:\n%s", want, err)
		}
	}
}

// The ordinary refusal still offers the other engine.
//
// The change above must not become "no refusal offers a way out": `RUN
// --privileged` is a real native-engine gap, the other engine does run it, and
// dropping that line would trade a false remedy for a missing one.
func TestARefusalForAnEngineGapStillOffersTheOtherEngine(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --privileged true\n", testMain)
	if err == nil {
		t.Fatal("RUN --privileged was accepted")
	}

	if !strings.Contains(err.Error(), "--engine=buildkit") {
		t.Errorf("a genuine engine gap no longer says where it does work:\n%s", err)
	}
}

// A refusal that is a decision does not read as a gap.
//
// `SAVE ARTIFACT --force` permits a save outside the directory holding the
// Earthfile, which is the thing three separate checks exist to stop - the
// interpreter's, the CLI's, and `insideProject` at the point of writing, the
// last of which resolves symlinks precisely so the position cannot be walked
// around. It is not missing. It is declined.
//
// The table recording what each refused flag asks for already says so: *"Refusing
// this one is a position rather than a gap ... 'Not supported' invites somebody
// to implement it."* The message did not, and a refusal reading as a gap is a
// standing invitation to close it - which here means removing a safety property
// on the grounds that the engine looked unfinished.
//
// Three kinds of refusal now, and they differ in what they promise:
//
//   - a gap, which arrives later and meanwhile runs elsewhere (unsupported);
//   - something the language does not have, where "elsewhere" is false and the
//     alternative is another construct (notInLanguage, E152);
//   - a decision, which arrives nowhere.
//
// The other engine is still named. It does permit this, and a reader who needs
// it should not have to discover that by trying - hiding it would be a lie by
// omission, which is what I10 rules out. Named as a disclosure, not as advice.
func TestARefusalThatIsADecisionSaysSoRatherThanReadingAsAGap(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN touch x\n    SAVE ARTIFACT --force x AS LOCAL out\n", testMain)
	if err == nil {
		t.Fatal("SAVE ARTIFACT --force was accepted")
	}

	if strings.Contains(err.Error(), "not supported") {
		t.Errorf("a deliberate refusal reads as an unfinished one:\n%s", err)
	}

	if !strings.Contains(err.Error(), "on purpose") {
		t.Errorf("the refusal does not say it is deliberate, so the reader cannot"+
			" tell it from work not yet done:\n%s", err)
	}

	// The policy itself, not just its name: a reader who disagrees needs to know
	// what they would be switching off.
	if !strings.Contains(err.Error(), "outside the project") {
		t.Errorf("the refusal does not say which position it is defending:\n%s", err)
	}

	if !strings.Contains(err.Error(), "--engine=buildkit") {
		t.Errorf("the refusal hides that another engine permits this:\n%s", err)
	}
}

// The refusal for `--privileged` says what a step already has.
//
// Measured, not assumed (E157): a step in this engine holds `CapEff
// 000001ffffffffff` - every capability there is - and can mount a tmpfs. What
// it cannot do is reach past its namespace, which `mknod` of a device node
// demonstrates with EPERM. Capabilities are namespaced; root in a user
// namespace is not root.
//
// So "not supported by the native engine" was wrong in both halves. There is
// nothing to implement - the capability set is already full - and switching
// engines is not the remedy for most of these: the corpus's own instance is
// `RUN --privileged echo "hello …" > a.txt`, which needs no privilege at all.
//
// The refusal therefore says the one thing that gets that build running:
// **remove the flag**. And it says what genuinely is not available, so a step
// that really does want a device knows it is asking the wrong engine rather
// than hitting a gap that might close next release.
func TestThePrivilegedRefusalSaysWhatTheStepAlreadyHas(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --privileged make\n", testMain)
	if err == nil {
		t.Fatal("RUN --privileged was accepted")
	}

	if strings.Contains(err.Error(), "not supported") {
		t.Errorf("it reads as unfinished work, and there is nothing to finish:\n%s", err)
	}

	for _, want := range []string{
		"every capability", // what the step already has
		"remove the flag",  // what to do about the common case
		"device",           // what is genuinely unavailable
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// A deliberate refusal is its own kind of number.
//
// The corpus report has three buckets - work to do, something the caller
// withheld, and invalid input - and a construct this engine declines belongs in
// none of them. It went to invalid input by default, under a heading reading
// *"verify these are right"*, which is the same mistake E151 fixed for
// `--required` ARGs: a thing that is not wrong, filed with the things that are,
// where nobody reads it.
//
// `ErrOnPurpose` wraps `ErrRefused` and is disjoint from `ErrUnimplemented`.
// Both distinctions matter: a caller asking "was this refused?" must still get
// yes, and a report ranking what to build next must not list a decision.
func TestADeliberateRefusalIsNotWorkAndNotInvalidInput(t *testing.T) {
	t.Parallel()

	for name, src := range map[string]string{
		"privileged": "\nmain:\n    FROM alpine:3.22\n    RUN --privileged make\n",
		"force":      "\nmain:\n    FROM alpine:3.22\n    RUN make\n    SAVE ARTIFACT --force /out\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+src, testMain)
			if err == nil {
				t.Fatal("accepted")
			}

			if !errors.Is(err, interp.ErrOnPurpose) {
				t.Errorf("a deliberate refusal is not marked as one:\n%s", err)
			}

			if !errors.Is(err, interp.ErrRefused) {
				t.Errorf("it left the refused family:\n%s", err)
			}

			if errors.Is(err, interp.ErrUnimplemented) {
				t.Errorf("a decision is counted as work still to do:\n%s", err)
			}
		})
	}
}

// Every refusal is exactly one of the three kinds.
//
// Green paper I10 now says what to do is a gap, a construct the language does
// not have, or a decision - and that which one it is *is part of the claim*: it
// decides whether a reader tries the other engine, rewrites the line, or stops.
//
// A refusal belonging to none of the three has a kind nobody chose, and one
// belonging to two has a kind nobody can act on. Neither is visible in the
// message, which is why this is asserted over the sentinels rather than read.
//
// Over the real refusals rather than constructed ones: the corpus is 192
// Earthfiles written without knowledge of this engine, and it produces refusals
// nobody here thought to write down.
func TestEveryRefusalIsExactlyOneKind(t *testing.T) {
	t.Parallel()

	for name, src := range map[string]string{
		// `RUN --ssh` was the gap here until it was implemented (E466), then
		// `--aws` until it was too. `--oidc` is one still, and asks for
		// credentials from a federated session this engine cannot open.
		"a gap":                  "\nmain:\n    FROM alpine:3.22\n    RUN --oidc thing make\n",
		"not in the language":    "\nmain:\n    FROM alpine:3.22\n    COPY --from=other /a /b\n",
		"a decision":             "\nmain:\n    FROM alpine:3.22\n    RUN make\n    SAVE ARTIFACT --force /out\n",
		"privileged, a decision": "\nmain:\n    FROM alpine:3.22\n    RUN --privileged make\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(versioned+src, testMain)
			if err == nil {
				t.Fatal("accepted")
			}

			if !errors.Is(err, interp.ErrRefused) {
				t.Fatalf("not a refusal at all:\n%s", err)
			}

			kinds := 0

			for _, k := range []error{
				interp.ErrUnimplemented,
				interp.ErrNotInLanguage,
				interp.ErrOnPurpose,
			} {
				if errors.Is(err, k) {
					kinds++
				}
			}

			if kinds != 1 {
				t.Errorf("belongs to %d of the three kinds, and I10 says exactly"+
					" one:\n%s", kinds, err)
			}
		})
	}
}
