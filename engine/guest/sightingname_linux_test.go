//go:build linux

package guest

import "testing"

// TestAPathIsJudgedByTheNameTheBaseWouldHoldItUnder.
//
// **The tracer reports a path by either of two names.** It resolves what it
// sees from outside the step's root, so the same file arrives sometimes as
// `/proc/10/mounts` and sometimes as
// `<mounts>/h-3452187907/merged/proc/10/mounts`. The second is renamed to the
// first before being recorded, because the outside name carries a per-build id
// and could match nothing later.
//
// The exclusion for what this engine mounted - the resolver, `/proc`, `/dev`, a
// cache directory - ran *before* that rename, so it only ever saw the first
// form. A path that arrived by its outside name walked straight past it and was
// recorded under its inside name.
//
// What that costs is a step that is stale for ever. `/proc/10/mounts` has a pid
// in it; no later build has that pid, so the prediction can never hold. Found
// in 4 of the 12 profiles written in half an hour of Rust builds, alongside
// `/proc/130/maps` and `/proc/11/fd` - glibc and jemalloc read their own
// `/proc/<pid>/*` at startup, so almost anything provokes it.
//
// The `own` check beside it was already placed after the rename, and says why:
// "the question is about the name the base would hold it under". This is the
// same question.
func TestAPathIsJudgedByTheNameTheBaseWouldHoldItUnder(t *testing.T) {
	t.Parallel()

	const root = "/var/lib/earthbuild/scratch/mounts/h-3452187907/merged"

	provided := []string{"/proc", "/dev", "/etc/resolv.conf"}

	for _, c := range []struct {
		seen string
		want bool
		why  string
	}{
		{"/proc/10/mounts", false, "named from inside, already excluded"},
		{root + "/proc/10/mounts", false, "named from outside, and the same file"},
		{root + "/dev/null", false, "the same, for a device"},
		{root + "/etc/resolv.conf", false, "the same, for the resolver"},
		{root + "/usr/lib/libc.so", true, "a real file of the base, named from outside"},
		{"/usr/lib/libc.so", true, "the same file, named from inside"},
	} {
		_, got := worthRecording(c.seen, root, provided)
		if got != c.want {
			t.Errorf("%q: recorded=%v, wanted %v (%s)", c.seen, got, c.want, c.why)
		}
	}
}

// TestTheNameKeptIsTheInsideOne, because the outside one carries a per-build id
// and would match nothing on any later build.
func TestTheNameKeptIsTheInsideOne(t *testing.T) {
	t.Parallel()

	const root = "/var/lib/earthbuild/scratch/mounts/h-99/merged"

	got, ok := worthRecording(root+"/usr/lib/libc.so", root, nil)
	if !ok {
		t.Fatal("a real file was not recorded")
	}

	if got != "/usr/lib/libc.so" {
		t.Errorf("recorded as %q, wanted the name the base holds it under", got)
	}
}
