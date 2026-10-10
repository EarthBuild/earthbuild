package guest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/nstest"
	"golang.org/x/sys/unix"
)

// What privilege this engine can actually hand a step, measured.
//
// `RUN --privileged` is the last construct the corpus reports as unimplemented,
// and "implement it" is only the right answer if there is something to
// implement. A step here runs with whatever the guest has: `isolate` adds mount,
// pid, uts and ipc namespaces and chroots, and it does **not** add
// CLONE_NEWUSER - the guest is already inside one, mapped to root (E105).
//
// Root in a user namespace is not root. Capabilities are namespaced: they
// authorise operations on objects the namespace owns, and refuse the ones that
// reach past it. So the question is not "does the step have CAP_SYS_ADMIN" -
// it does - but "what does having it fail to buy".
//
// This is the measurement, not an argument. It is what decides whether
// `--privileged` is a gap to close, a flag asking for what the engine already
// gives (the `--keep-ts` case, E68's expensive direction), or something this
// engine cannot grant at all.
func TestWhatPrivilegeAStepCanBeGiven(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	dir := t.TempDir()

	t.Run("a full capability set", func(t *testing.T) {
		b, err := os.ReadFile("/proc/self/status")
		if err != nil {
			t.Fatal(err)
		}

		var eff string

		for line := range strings.SplitSeq(string(b), "\n") {
			if rest, found := strings.CutPrefix(line, "CapEff:"); found {
				eff = strings.TrimSpace(rest)
			}
		}

		if eff == "" || eff == "0000000000000000" {
			t.Errorf("no effective capabilities at all (CapEff %q), so the premise"+
				" of this measurement is wrong", eff)
		}

		t.Logf("CapEff %s - a full set inside the namespace", eff)
	})

	// A tmpfs mount is authorised: the namespace owns its own mount table, so
	// CAP_SYS_ADMIN means something here. This is the half that works, and it
	// is why rootless `apt` and overlayfs work at all.
	t.Run("mounting a tmpfs", func(t *testing.T) {
		at := filepath.Join(dir, "tmp")
		err := os.Mkdir(at, 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = unix.Mount("tmpfs", at, "tmpfs", 0, "")
		if err != nil {
			t.Errorf("a namespace-owned mount was refused: %v", err)

			return
		}

		_ = unix.Unmount(at, 0)
	})

	// A device node is not, and this is the decisive one: mknod of a character
	// device is refused inside a user namespace whatever the capability set
	// says, because the device belongs to the host and not to the namespace.
	//
	// A build step that asks for `--privileged` in order to reach a device -
	// which is most of why anybody asks - cannot be served here by any amount
	// of implementation.
	t.Run("making a device node", func(t *testing.T) {
		at := filepath.Join(dir, "null")

		err := unix.Mknod(at, unix.S_IFCHR|0o666, int(unix.Mkdev(1, 3)))
		if err == nil {
			t.Errorf("a device node was created inside a user namespace, which" +
				" contradicts what the refusal for --privileged is based on")

			_ = os.Remove(at)

			return
		}

		if !errors.Is(err, unix.EPERM) {
			t.Logf("mknod refused with %v rather than EPERM", err)
		}

		t.Logf("mknod: %v - the namespace cannot hand out a device", err)
	})
}
