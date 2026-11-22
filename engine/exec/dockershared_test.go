package exec

import (
	"strings"
	"testing"
)

// A step promised an empty daemon is not given a shared one.
//
// **Two decisions that were made separately and contradict.** The interpreter
// says a `WITH DOCKER` block with no `--cache-id` starts with an empty daemon
// and is therefore **cacheable** (E354). The executor's only daemon today is
// *this machine's*, lent behind an operator opt-in - which holds whatever every
// previous build left in it.
//
// So a step declared to be a function of its inputs is handed state that is not
// its inputs, and its result is cached under a key that says nothing about what
// it saw. That is a false cache hit waiting for the second build (I3), and it is
// not a daemon problem: it is two correct decisions meeting.
//
// The refusal names the way out that exists today - a `--cache-id`, which makes
// the block honestly uncacheable - rather than only the one that does not
// (E355).
func TestAStepPromisedAnEmptyDaemonIsNotGivenASharedOne(t *testing.T) {
	t.Parallel()

	_, _, err := sharedDockerFor("", func(string) (string, bool) {
		return "/usr/bin/docker", true
	}, true, Readiness{OK: true})
	if err == nil {
		t.Fatal("a cacheable block was given this machine's daemon, so its" +
			" result is cached under a key that says nothing about what the" +
			" daemon held (E355)")
	}

	for _, want := range []string{"--cache-id", "cache"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// A block that shares a cache may have a shared daemon.
//
// It is already uncacheable by construction (E354), so the daemon holding what
// an earlier build left is what it asked for - and refusing here would refuse
// the only configuration that works today.
func TestASharedBlockMayHaveASharedDaemon(t *testing.T) {
	t.Parallel()

	got, _, err := sharedDockerFor("layers", func(string) (string, bool) {
		return "/usr/bin/docker", true
	}, true, Readiness{OK: true})
	if err != nil {
		t.Fatalf("a block sharing a cache was refused a shared daemon: %v", err)
	}

	if len(got) == 0 {
		t.Error("no mounts were given to a block that asked to share")
	}
}

// The opt-in still comes first.
//
// A step holding this machine's socket has root on this machine whatever its
// cache says, so the trust gate is not something a `--cache-id` buys past
// (E145).
func TestTheHostDaemonGateComesBeforeTheCacheQuestion(t *testing.T) {
	t.Parallel()

	_, _, err := sharedDockerFor("layers", func(string) (string, bool) {
		return "/usr/bin/docker", true
	}, false, Readiness{OK: true})
	if err == nil {
		t.Fatal("a shared block reached this machine's daemon without the" +
			" operator allowing it")
	}

	if !strings.Contains(err.Error(), envAllowHostDocker) {
		t.Errorf("the refusal does not name the variable:\n%s", err)
	}
}

// A cache name a peer sent is checked before this machine acts on it.
//
// The interpreter checks the same string and that check is for the author
// (E358). This one is for the machine: a step assignment arrives from a driver
// this worker did not write (A5, C.3), so by the time the executor is asked to
// give a block its cache, the name is a claim - and it is the name a directory
// will be made of the moment there is a daemon to give one (E360).
func TestACacheNameFromAPeerIsCheckedHere(t *testing.T) {
	t.Parallel()

	_, _, err := sharedDockerFor("../../etc", func(string) (string, bool) {
		return "/usr/bin/docker", true
	}, true, Readiness{OK: true})
	if err == nil {
		t.Fatal("a block naming ../../etc as its cache was served")
	}

	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("the refusal does not say what was wrong with the name:\n%s", err)
	}
}

// A cache this engine cannot give says so, rather than being taken for one.
//
// **`--cache-id=a` and `--cache-id=b` get the same storage today.** The only
// daemon on Linux is this machine's, which has one storage area that every block
// shares - so two names that promise two caches deliver one, and a build that
// separated its caches on purpose did not.
//
// The promise is E354's: blocks naming the same cache see each other's images,
// and blocks naming different ones do not. Half of it holds and half does not,
// and which half is not something an author can see from the Earthfile (E362).
//
// Said as a note rather than a refusal: the block still works, the sharing it
// asked for still happens, and what it does not get is the *separation*. A
// refusal would take away the only configuration that runs today for the sake of
// a property most uses do not rely on.
func TestACacheThisEngineCannotGiveSaysSo(t *testing.T) {
	t.Parallel()

	_, note, err := sharedDockerFor("layers", func(string) (string, bool) {
		return "/usr/bin/docker", true
	}, true, Readiness{OK: true})
	if err != nil {
		t.Fatalf("%v", err)
	}

	for _, want := range []string{"layers", "every"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not mention %q:\n%s", want, note)
		}
	}
}

// A block that named no cache is not told about one.
//
// It never asked for separation, so a note about not getting it is noise - and
// noise in the one place this engine has for saying why a daemon behaved oddly
// is how that place stops being read (E146).
func TestABlockThatNamedNoCacheIsNotToldAboutOne(t *testing.T) {
	t.Parallel()

	// A block with no cache is refused a shared daemon (E355), so the note is
	// asked of the only other caller: one that names a cache and is served.
	_, note, err := sharedDockerFor("", func(string) (string, bool) {
		return "/usr/bin/docker", true
	}, true, Readiness{OK: true})
	if err == nil {
		t.Fatal("a block naming no cache was given a shared daemon")
	}

	if strings.Contains(note, "every block") {
		t.Errorf("a block that asked for nothing was told about sharing:\n%s",
			note)
	}
}
