package subcmd

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/cmd/earth/flag"
)

// A flag this engine cannot honour says so, rather than being accepted and
// doing nothing.
//
// **Silence is the failure mode worth fixing.** `--remote-cache` on the native
// engine used to be parsed, stored and never read: the build succeeded, the
// cache was not shared, and nothing in the output suggested otherwise. That is
// the same shape as `SAVE IMAGE --push` before it published anything.
func TestAFlagThisEngineIgnoresSaysSo(t *testing.T) {
	t.Parallel()

	said := ignoredNote(wasSet("remote-cache"))
	if said == "" {
		t.Fatal("--remote-cache was ignored silently")
	}

	// The flag by the name the user typed, and why - a warning that says only
	// "unsupported" sends the reader to the source to find out what they lost.
	for _, want := range []string{"--remote-cache", "cache"} {
		if !strings.Contains(said, want) {
			t.Errorf("the note does not mention %q: %s", want, said)
		}
	}
}

// A flag left at its default is not a flag the user passed, and warning about
// it would bury the one they did.
func TestUnsetFlagsAreNotMentioned(t *testing.T) {
	t.Parallel()

	if said := ignoredNote(wasSet()); said != "" {
		t.Errorf("an invocation that set nothing was warned about: %s", said)
	}

	// **A flag left at its default is not a flag the user set.** Several of
	// these declare one - a container name, a frontend - so reading the parsed
	// struct instead of asking the parser warned about five flags nobody typed,
	// at every single invocation.
	if said := ignoredNote(func(string) bool { return false }); said != "" {
		t.Errorf("defaults were reported as choices: %s", said)
	}
}

// Several at once are one note, not several: a build machine passing a handful
// of buildkit flags should not have its output filled with them.
func TestSeveralIgnoredFlagsAreOneNote(t *testing.T) {
	t.Parallel()

	said := ignoredNote(wasSet("remote-cache", "use-inline-cache", "pull"))

	if n := strings.Count(said, "\n"); n > 5 {
		t.Errorf("%d lines for three flags:\n%s", n+1, said)
	}

	for _, want := range []string{"--remote-cache", "--use-inline-cache", "--pull"} {
		if !strings.Contains(said, want) {
			t.Errorf("the note does not mention %q:\n%s", want, said)
		}
	}
}

// Every flag is classified: read by the native path, reported as ignored, or
// declared to be about something other than the build.
//
// **So the next flag added has to be decided about.** The gap this closes was
// not that any one flag was missed - it was that missing one cost nothing and
// showed nothing. Reading the native path's own source for the flags it touches
// means the honoured set cannot drift from the code that honours it.
func TestEveryFlagIsClassified(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("build_native.go")
	if err != nil {
		t.Fatal(err)
	}

	read := map[string]bool{}
	for _, m := range regexp.MustCompile(`Flags\(\)\.([A-Z][A-Za-z0-9]*)`).FindAllStringSubmatch(string(src), -1) {
		read[m[1]] = true
	}

	if len(read) == 0 {
		t.Fatal("no flags found in build_native.go, so this guard proves nothing")
	}

	for f := range reflect.TypeFor[flag.Global]().Fields() {
		name := f.Name
		if _, ignored := ignoredByNative[name]; ignored || read[name] || notAboutTheBuild[name] {
			continue
		}

		t.Errorf("flag.Global.%s is neither read by the native path, nor listed"+
			" in ignoredByNative, nor declared not to be about the build"+
			"\n  add it to one of the three: a flag nobody decided about is one"+
			" that is accepted and does nothing", name)
	}
}

// And nothing is claimed ignored that the native path in fact reads - the note
// would then be a lie in the other direction.
func TestNothingIsCalledIgnoredThatIsRead(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("build_native.go")
	if err != nil {
		t.Fatal(err)
	}

	for name := range ignoredByNative {
		if strings.Contains(string(src), "Flags()."+name) {
			t.Errorf("flag.Global.%s is listed as ignored and the native path reads it", name)
		}
	}
}

// wasSet is the parser's answer, faked: these are the flags the user typed.
func wasSet(names ...string) func(string) bool {
	typed := map[string]bool{}
	for _, n := range names {
		typed[n] = true
	}

	return func(n string) bool { return typed[n] }
}
