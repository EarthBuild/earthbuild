package guest

import (
	"runtime"
	"strings"
	"testing"
)

// The entries become a hosts file the step resolves by.
//
// Written rather than appended to the image's own: an image ships an
// `/etc/hosts` and a step that resolved by a *merged* file would resolve
// differently depending on what its base happened to contain, which is ambient
// state the key does not describe (I3). The file a step gets is a function of
// what the Earthfile said and nothing else.
//
// Localhost is in it because a file without it breaks anything that resolves
// `localhost`, which is most things - and the entries a build declares are
// additions to a working resolver, not a replacement for one.
func TestTheHostsFileIsWhatTheEarthfileSaid(t *testing.T) {
	t.Parallel()

	got := hostsFile([]string{"api.test 10.0.0.1", "db.test 10.0.0.2"})

	for _, want := range []string{
		"127.0.0.1\tlocalhost",
		"10.0.0.1\tapi.test",
		"10.0.0.2\tdb.test",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the hosts file has no %q:\n%s", want, got)
		}
	}

	// The address first, which is the file's format and not the command's: a
	// line written the other way round resolves nothing and looks right.
	if strings.Contains(got, "api.test\t10.0.0.1") {
		t.Errorf("an entry is written name-first, which no resolver reads:\n%s", got)
	}

	if !strings.HasSuffix(got, "\n") {
		t.Error("the file does not end in a newline, and a resolver drops the last line")
	}
}

// No entries, but still the two names every step is entitled to.
//
// **This asserted "no entries, no file" until E768.** That rule read well - a
// step gets what its image ships rather than an engine invention - and it left
// a step unable to resolve its own name, which `earth-entrypoint.sh` turns into
// the address of the inner build's daemon. Five Native jobs waited a minute
// each for a name nothing answered.
//
// The reasoning survives where it applies: declared entries are still *written*
// rather than merged with the image's, so what a step resolves by is what the
// Earthfile said. It now also gets localhost and its own name, which no
// Earthfile should have to declare.
func TestNoEntriesStillMeansAResolvableSandbox(t *testing.T) {
	t.Parallel()

	got := hostsFile(nil)

	for _, want := range []string{"127.0.0.1\tlocalhost\n", "127.0.0.1\t" + SandboxHost + "\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("a step declaring nothing cannot resolve %q:\n%s", want, got)
		}
	}
}

// A step that declared entries is given a mount carrying them.
//
// The composition, asserted without a running guest. The mutation sweep deleted
// it and nothing failed, because the only test that exercised it was behind the
// `integration` tag - and a sweep that does not build with tags cannot see a
// mechanism only a tagged test guards.
func TestDeclaredHostsTravelAsAMount(t *testing.T) {
	t.Parallel()

	got := hostsMount([]string{"api.test 10.0.0.1"})

	if len(got) != 1 {
		t.Fatalf("a step declaring an entry got %d mount(s)", len(got))
	}

	if got[0].Target != "/etc/hosts" {
		t.Errorf("the mount lands at %q, where no resolver looks", got[0].Target)
	}

	if !strings.Contains(got[0].Secret, "10.0.0.1\tapi.test") {
		t.Errorf("the mount does not carry the entry: %q", got[0].Secret)
	}

	// Since E768 a step that declared nothing gets one too, carrying the two
	// names it is entitled to and nothing else.
	bare := hostsMount(nil)
	if len(bare) != 1 {
		t.Fatalf("a step declaring nothing got %d mount(s), want 1", len(bare))
	}

	if strings.Contains(bare[0].Secret, "api.test") {
		t.Error("a step was given another step's entries")
	}
}

// And a step's mounts include them.
//
// The composition rather than the piece: deleting the line that adds the hosts
// to a step's mounts left every test of `hostsMount` green, because none of them
// asked what a *step* is given (E415).
func TestAStepsMountsIncludeItsDeclaredHosts(t *testing.T) {
	t.Parallel()

	var found bool

	for _, m := range stepMounts(Request{Hosts: []string{"api.test 10.0.0.1"}}, nil, false) {
		if m.Target == "/etc/hosts" {
			found = true
		}
	}

	if !found {
		t.Error("a step declaring host entries is given no /etc/hosts, so it" +
			" resolves by whatever its image shipped")
	}

	// And a step that declared nothing gets one as well, because its own name
	// has to resolve whether or not an Earthfile mentioned any (E768). On a
	// platform with no mounts it gets none, which is what hostsMountFor is for.
	if runtime.GOOS == "linux" {
		var bare bool

		for _, m := range stepMounts(Request{}, nil, false) {
			if m.Target == "/etc/hosts" {
				bare = true
			}
		}

		if !bare {
			t.Error("a step declaring nothing cannot resolve its own name")
		}
	}
}
