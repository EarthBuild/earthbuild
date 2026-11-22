package exec

import (
	"strings"
	"testing"
)

// A daemon of a step's own is given the cache it was named, and nothing else.
//
// **The mounts, which are fewer than the experiment suggested** (E364). That
// attempt mounted a tmpfs over `/run`, because it ran in the *host's* mount
// namespace where `/run` belongs to the machine. A step's root is an overlay: it
// is writable already, and the tmpfs was an artefact of the experiment rather
// than a requirement of the daemon (E365).
//
// What is left is the storage. A block naming a cache gets that directory at a
// path inside the sandbox and points the daemon's data root at it; a block
// naming none gets nothing, and its daemon writes into the step's own root,
// which is thrown away with the step - which is what "isolated" means (E354).
func TestAStepsOwnDaemonIsGivenItsCacheAndNothingElse(t *testing.T) {
	t.Parallel()

	shared, root := ownDaemonMounts("layers", "")

	if len(shared) != 1 {
		t.Fatalf("a block naming a cache got %d mount(s), want 1: %v",
			len(shared), shared)
	}

	if !strings.Contains(shared[0].ID, "layers") {
		t.Errorf("the mount does not name the cache: %q", shared[0].ID)
	}

	if !strings.HasPrefix(root, "/") || !strings.HasPrefix(shared[0].Target, "/") {
		t.Errorf("a daemon root inside a step must be absolute: %q, %q",
			root, shared[0].Target)
	}

	if !strings.HasPrefix(root, shared[0].Target) {
		t.Errorf("the daemon's root %q is not under the cache mounted at %q",
			root, shared[0].Target)
	}
}

// A block naming no cache gets a mount that is thrown away, not no mount.
//
// **This reverses E365**, and a real build reversed it. The reasoning then was
// that mounting nothing leaves the daemon's storage in the step's own overlay,
// to be discarded with the step. A step's overlay is exactly what the capture
// turns into a layer, so what actually happened was that every isolated block
// shipped its whole docker store inside the image it produced - and left a
// `docker.pid` there, which made the next step refuse to start a daemon that was
// "already running" (E398).
//
// A mount is a hole in the step's filesystem: what the step writes into it is
// not part of what the step produced. That is what makes a cache mount work, and
// it is what "not captured" requires. Whether the directory *outlives* the step
// is a separate question, and the only one the cache name answers.
func TestAnIsolatedDaemonGetsAMountThatIsThrownAway(t *testing.T) {
	t.Parallel()

	got, root := ownDaemonMounts("", "")

	if len(got) != 1 {
		t.Fatalf("a block sharing nothing got %d mount(s), want 1: %v", len(got), got)
	}

	if !got[0].Ephemeral {
		t.Error("the daemon's storage is not ephemeral, so it outlives the step" +
			" it was made for")
	}

	if got[0].ID != "" {
		t.Errorf("an ephemeral mount names a stored directory, which would keep"+
			" it: %q", got[0].ID)
	}

	if got[0].Target != root {
		t.Errorf("the mount is at %s and the daemon's root is %s", got[0].Target, root)
	}
}

// A named cache is the other half: mounted, and *not* thrown away.
func TestANamedCacheIsNotThrownAway(t *testing.T) {
	t.Parallel()

	got, _ := ownDaemonMounts("layers", "")

	if len(got) != 1 {
		t.Fatalf("%d mount(s), want 1", len(got))
	}

	if got[0].Ephemeral {
		t.Error("a named cache would be removed with the step, so it would share" +
			" nothing with the next build - which is the whole point of naming it")
	}
}

// Two names are two mounts, and the daemon is pointed at each.
//
// The half of `--cache-id` the host's daemon cannot give (E362): different names
// are different directories, so blocks naming different caches do not see each
// other's images.
func TestTwoNamesGiveTwoDaemonRoots(t *testing.T) {
	t.Parallel()

	one, _ := ownDaemonMounts("a", "")
	two, _ := ownDaemonMounts("b", "")

	if one[0].ID == two[0].ID {
		t.Errorf("two named caches mount the same directory: %q", one[0].ID)
	}
}
