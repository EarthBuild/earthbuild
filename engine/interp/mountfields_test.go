package interp_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// mountLine plans one RUN with the given mount specification.
func mountLine(spec string) (mounts []mountShape, err error) {
	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --mount="+spec+" make\n", testMain)
	if err != nil {
		return nil, err
	}

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			mounts = append(mounts, mountShape{
				Target: m.Target, ID: m.ID,
				Exclusive: m.Exclusive, Ephemeral: m.Ephemeral, ReadOnly: m.ReadOnly,
			})
		}
	}

	return mounts, nil
}

type mountShape struct {
	Target    string
	ID        string
	Exclusive bool
	Ephemeral bool
	ReadOnly  bool
}

// `RUN --mount` honours `sharing`, which it was reading and discarding.
//
// The field is Docker's and the shipping engine's, and it means what `CACHE
// --sharing` means. Dropped silently, `sharing=locked` produced a directory
// several steps could be in at once - **an option accepted and not provided**,
// which is the failure E427 and E432 each fixed once already, here in the one
// place nobody had looked (E435).
//
// `shared` is the default for this form, and `locked` for `CACHE`. Not an
// inconsistency to tidy: they are two commands with two upstream defaults, and
// changing either would make an Earthfile mean something different here from
// what it means everywhere else.
func TestRunMountHonoursSharing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		spec string
		want mountShape
	}{{
		spec: "type=cache,target=/c",
		want: mountShape{Target: "/c", ID: "c"},
	}, {
		spec: "type=cache,target=/c,sharing=shared",
		want: mountShape{Target: "/c", ID: "c"},
	}, {
		spec: "type=cache,target=/c,sharing=locked",
		want: mountShape{Target: "/c", ID: "c", Exclusive: true},
	}, {
		spec: "type=cache,target=/c,sharing=private",
		want: mountShape{Target: "/c", Ephemeral: true},
	}} {
		got, err := mountLine(tc.spec)
		if err != nil {
			t.Errorf("%s: %v", tc.spec, err)

			continue
		}

		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s planned %+v, want %+v", tc.spec, got, tc.want)
		}
	}
}

// A field this engine does not provide is refused, by name.
//
// Every key but five was read into a map and never looked at again, so
// `mode=0700` planned a mount without the mode and `sharing=locked` a cache
// without the lock. The map is what made it silent: parsing something is not
// providing it, and a parser that collects everything and consults some of it
// cannot tell the two apart.
//
// Refused rather than ignored because the direction matters (E34): refusing a
// field this engine could have honoured costs a build that says what is missing,
// and ignoring one costs a step that ran with something other than what it asked
// for - and reports success.
func TestAnUnprovidedMountFieldIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ spec, names string }{
		{"type=cache,target=/c,uid=1000", "uid"},
		{"type=cache,target=/c,gid=1000", "gid"},
		{"type=cache,target=/c,from=+builder", "from"},
		{"type=cache,target=/c,sharing=occasionally", "sharing"},
		{"type=secret,target=/s,id=tok,required=true", "required"},
		{"type=cache,target=/c,unheardof=1", "unheardof"},
	} {
		_, err := mountLine(tc.spec)
		if err == nil {
			t.Errorf("%s: planned without complaint, and the field was dropped", tc.spec)

			continue
		}

		if !strings.Contains(err.Error(), tc.names) {
			t.Errorf("%s: refused with %q, which does not name the field", tc.spec, err)
		}
	}
}

// `ro` is `readonly`, and a bare flag is true.
//
// Both forms are Docker's, and `readonly` alone - no `=true` - is how it is
// usually written. Compared against the string "true", a bare `ro` was false,
// so the mount the step asked to be read-only was writable and the step's writes
// went somewhere it believed it could not write.
func TestReadOnlyIsSpeltBothWays(t *testing.T) {
	t.Parallel()

	for _, spec := range []string{
		"type=cache,target=/c,readonly=true",
		"type=cache,target=/c,readonly",
		"type=cache,target=/c,ro",
	} {
		got, err := mountLine(spec)
		if err != nil {
			t.Errorf("%s: %v", spec, err)

			continue
		}

		if len(got) != 1 || !got[0].ReadOnly {
			t.Errorf("%s planned %+v, which is writable", spec, got)
		}
	}
}

// `mode` and `chmod` set the permissions of what is mounted.
//
// Real Earthfiles in this repository's own corpus write `type=secret,mode=0100`,
// and a secret staged 0644 where the author asked for 0100 is a credential
// readable by a user the author excluded. That is why this is implemented rather
// than refused: the field is in use, it is well defined, and dropping it is the
// silent-wrong direction (E435).
//
// `chmod` is the same field under the other spelling, which the corpus also
// uses.
func TestAMountsModeIsCarried(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		spec string
		want uint32
	}{
		{"type=cache,target=/c", 0},
		{"type=cache,target=/c,mode=0755", 0o755},
		{"type=cache,target=/c,chmod=0700", 0o700},
		{"type=cache,target=/c,mode=755", 0o755},
		// `mode=$mode` with the argument unsupplied, which is what
		// `tests/cache-mount-mode.earth` is: the spec is expanded before it is
		// parsed, so an empty field and an unwritten one are the same string by
		// the time anything here can tell them apart. Refusing it would refuse
		// the file for what the expansion did.
		{"type=cache,target=/c,mode=", 0},
	} {
		got, err := modeOf(tc.spec)
		if err != nil {
			t.Errorf("%s: %v", tc.spec, err)

			continue
		}

		if got != tc.want {
			t.Errorf("%s planned mode %#o, want %#o", tc.spec, got, tc.want)
		}
	}
}

// A mode that is not a mode is refused, saying what was read.
//
// Parsed with an explicit base of 8, so `0644` and `644` mean the same thing.
// A mode misread as decimal is a permission nobody asked for, which is the same
// silent-wrong failure one layer down.
func TestAModeThatIsNotAModeIsRefused(t *testing.T) {
	t.Parallel()

	for _, spec := range []string{
		"type=cache,target=/c,mode=rwx",
		"type=cache,target=/c,mode=99999999",
		"type=cache,target=/c,mode=0888",
	} {
		_, err := modeOf(spec)
		if err == nil {
			t.Errorf("%s: planned without complaint", spec)

			continue
		}

		if !strings.Contains(err.Error(), "mode") {
			t.Errorf("%s: refused with %q, which does not name the field", spec, err)
		}
	}
}

// modeOf plans one mount and returns its mode.
func modeOf(spec string) (uint32, error) {
	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --mount="+spec+" make\n", testMain)
	if err != nil {
		return 0, err
	}

	for _, n := range p.Graph.Nodes() {
		for _, m := range n.Op.Mounts {
			return m.Mode, nil
		}
	}

	return 0, nil
}

// `CACHE --chmod` reaches the mount.
//
// Found by the flag sweep, in the command this work had just spent two
// increments on: the option is in the parser, has been since before this engine,
// and nothing read it (E436).
//
// Its parser default is `0644`, which is not a mode any *directory* can be used
// with - no execute bit means nothing can enter it. So the default is treated as
// unwritten, and an author who writes it literally is treated the same way. That
// conflation is deliberate and it is the kind answer: the alternative is a cache
// nobody can cd into, produced by a flag they did not know they had.
func TestCacheChmodReachesTheMount(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		line string
		want uint32
	}{
		{"CACHE /c", 0},
		{"CACHE --chmod=0644 /c", 0},
		{"CACHE --chmod=0755 /c", 0o755},
		{"CACHE --chmod=0700 /c", 0o700},
	} {
		p, err := interp.Build(versioned+
			"\nmain:\n    FROM alpine:3.22\n    "+tc.line+"\n    RUN make\n", testMain)
		if err != nil {
			t.Errorf("%s: %v", tc.line, err)

			continue
		}

		var got uint32

		for _, n := range p.Graph.Nodes() {
			for _, m := range n.Op.Mounts {
				got = m.Mode
			}
		}

		if got != tc.want {
			t.Errorf("%s planned mode %#o, want %#o", tc.line, got, tc.want)
		}
	}
}

// `RUN --push` does not run, and does not stop the build.
//
// It means *run this only when the build is invoked in push mode*, and this
// engine has no push mode. The flag appeared nowhere in the interpreter: parsed,
// dropped, and the command ran on every build (E436).
//
// Of everything the flag sweep found, this is the one that does damage rather
// than merely disappointing. `RUN --push ./publish.sh` is the shape the option
// exists for, and running it unasked is not a slower build or a colder cache -
// it is a release nobody authorised.
func TestRunPushIsPlannedAway(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+
		"\nmain:\n    FROM alpine:3.22\n    RUN --push ./publish.sh\n    RUN after\n",
		testMain)
	if err != nil {
		t.Fatalf("planning a target with a push command: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		for _, arg := range n.Op.Args {
			if strings.Contains(arg, "publish.sh") {
				t.Fatalf("the push command is in the plan as %v"+
					"\n  it would run on every build, which is what the flag"+
					" exists to prevent", n.Op.Args)
			}
		}
	}

	// And the build goes on. Planning it away must not take the rest of the
	// recipe with it: the commands after a push command stand on the filesystem
	// as it was before it, which is where the reference leaves them.
	var found bool

	for _, n := range p.Graph.Nodes() {
		found = found || slices.Contains(n.Op.Args, "after")
	}

	if !found {
		t.Error("the command after the push command is not in the plan either")
	}
}
