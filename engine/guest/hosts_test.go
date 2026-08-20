package guest

import (
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

// No entries, no file.
//
// A step that declared nothing must get whatever its image ships, not an engine
// invention - the same rule as every other piece of ambient state here.
func TestNoEntriesMeansNoFile(t *testing.T) {
	t.Parallel()

	if got := hostsFile(nil); got != "" {
		t.Errorf("a step that declared no hosts was given a file:\n%s", got)
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

	if len(hostsMount(nil)) != 0 {
		t.Error("a step that declared nothing was given a hosts mount")
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

	for _, m := range stepMounts(Request{Hosts: []string{"api.test 10.0.0.1"}}) {
		if m.Target == "/etc/hosts" {
			found = true
		}
	}

	if !found {
		t.Error("a step declaring host entries is given no /etc/hosts, so it" +
			" resolves by whatever its image shipped")
	}

	for _, m := range stepMounts(Request{}) {
		if m.Target == "/etc/hosts" {
			t.Error("a step declaring nothing was given a hosts file anyway")
		}
	}
}
