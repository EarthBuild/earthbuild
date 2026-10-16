//go:build darwin

package exec_test

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// A sandbox VM is named after what is baked into it, so the same inputs find
// the same VM and different inputs never share one.
//
// The name was derived from the process id, which made every invocation boot
// its own VM - 620-700ms of Apple's `container run`, measured, and the largest
// single cost in a one-line-change rebuild (E19). A name derived from the
// mounts instead lets the next build attach to the VM the last one left
// running, which is what makes reuse safe rather than merely fast: a VM with
// different mounts has a different name and is never mistaken for this one.
func TestASandboxIsNamedAfterWhatIsInIt(t *testing.T) {
	t.Parallel()

	base := exec.SandboxName("alpine:3.20", "/opt/earth", "/var/cache/store")

	if !strings.HasPrefix(base, "earthbuild-") {
		t.Errorf("the name does not say whose it is: %q", base)
	}

	if again := exec.SandboxName("alpine:3.20", "/opt/earth", "/var/cache/store"); again != base {
		t.Errorf("the same sandbox got two names: %q and %q", base, again)
	}

	for _, tc := range []struct{ what, image, dir, store string }{
		{"a different image", "alpine:3.21", "/opt/earth", "/var/cache/store"},
		{"a different guest directory", "alpine:3.20", "/opt/other", "/var/cache/store"},
		{"a different store", "alpine:3.20", "/opt/earth", "/var/cache/other"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			t.Parallel()

			if got := exec.SandboxName(tc.image, tc.dir, tc.store); got == base {
				t.Errorf("%s shares a VM with a different one: %q", tc.what, got)
			}
		})
	}
}

// The name is a container name, not a path or a digest with punctuation in it.
func TestASandboxNameIsUsableAsAContainerName(t *testing.T) {
	t.Parallel()

	name := exec.SandboxName("ghcr.io/earthbuild/guest:1.2", "/opt/a b", "/var/cache/store")

	for _, bad := range []string{"/", ":", " ", ".", "_"} {
		if strings.Contains(name, bad) {
			t.Errorf("the name contains %q, which a container name may not: %q", bad, name)
		}
	}

	if len(name) > 63 {
		t.Errorf("the name is %d characters, longer than a container name may be", len(name))
	}
}

// A VM whose owning process is gone is reaped.
//
// The old name was `earthbuild-<pid>-<n>`, and Start removed *its own* name
// before booting - which can never be a stale one, because the pid is this
// process. So nothing ever reaped anything: 38 orphaned VMs were found running
// on the development machine, each holding 1GB, all from runs whose process had
// long since exited. The comment claimed the pid was there so that a VM
// outliving a crashed engine would be reaped; it guaranteed the opposite.
func TestAnOrphanedVMIsRecognised(t *testing.T) {
	t.Parallel()

	self := os.Getpid()

	for _, tc := range []struct {
		name   string
		orphan bool
	}{
		{"earthbuild-999999999-0", true},
		{"earthbuild-" + strconv.Itoa(self) + "-0", false},
		{"earthbuild-abc123def4567890", false},
		{"something-else", false},
		{"earthbuild-", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := exec.IsOrphanedSandbox(tc.name); got != tc.orphan {
				t.Errorf("orphaned=%v, want %v", got, tc.orphan)
			}
		})
	}
}

// A content-named VM is never an orphan: it has no owning process by design,
// and reaping one would take the sandbox out from under a concurrent build in
// another project.
func TestAContentNamedVMIsNeverReaped(t *testing.T) {
	t.Parallel()

	if exec.IsOrphanedSandbox(exec.SandboxName("alpine:3.20", "/opt/earth", "/store")) {
		t.Error("the VM this scheme exists to keep would be reaped")
	}
}

// The container listing answers both questions this engine has about VMs.
//
// It used to ask twice: `container ls -a` to find orphans, then `container exec
// <name> true` to decide whether this build's VM was up. The second cost 50-70ms
// on every build that ran anything - measured - against 10-20ms for the listing,
// and the listing already knows. Asking once takes a quarter off the fixed cost
// of a rebuild that does work.
func TestTheContainerListingSaysWhatIsRunning(t *testing.T) {
	t.Parallel()

	const out = `ID                    IMAGE                          OS     ARCH   STATE    ADDR              CPUS  MEMORY
earthbuild-a1b2c3d4  docker.io/library/alpine:3.20  linux  arm64  running  192.168.64.90/24  4     1024 MB
earthbuild-99999-0   docker.io/library/alpine:3.20  linux  arm64  stopped  192.168.64.91/24  4     1024 MB
somebody-elses       docker.io/library/debian:12    linux  arm64  running  192.168.64.92/24  4     1024 MB
`

	got := exec.ParseContainers([]byte(out))

	for name, want := range map[string]string{
		"earthbuild-a1b2c3d4": "running",
		"earthbuild-99999-0":  "stopped",
		"somebody-elses":      "running",
	} {
		if got[name] != want {
			t.Errorf("%s is %q, want %q", name, got[name], want)
		}
	}

	if _, ok := got["ID"]; ok {
		t.Error("the header was read as a container")
	}

	if len(got) != 3 {
		t.Errorf("found %d containers, want 3: %v", len(got), got)
	}
}

// Output that is not a listing yields nothing rather than nonsense.
//
// The decision made from this is "boot a VM or attach to one", and a garbled
// line that produced a plausible name would attach to a machine that is not
// there - failing at the first step with a protocol error rather than booting.
func TestAnUnreadableListingYieldsNothing(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "\n\n", "error: cannot connect to the container service\n"} {
		got := exec.ParseContainers([]byte(in))
		for name, state := range got {
			if state == "running" {
				t.Errorf("%q was read as a running container from %q", name, in)
			}
		}
	}
}

// A VM started with different arguments is a different machine, and the name
// says so.
//
// The docker sandbox runs its daemon with the containerd image store enabled,
// because that is what makes `docker load` accept the OCI layout this engine
// writes. A VM already running without that flag answers the listing, gets
// reused, and then fails the load with a message about a missing `blobs/json`
// - the legacy format it fell back to. The command belongs in the name for the
// same reason the mounts do.
func TestTheKeepAliveCommandIsPartOfTheName(t *testing.T) {
	t.Parallel()

	plain := exec.SandboxName("docker:27-dind", "/opt/earth", "/store")
	withFlag := exec.SandboxNameWith("docker:27-dind", "/opt/earth", "/store", "8G",
		[]string{"--feature", "containerd-snapshotter=true"})

	if plain == withFlag {
		t.Error("a daemon with a different image store shares a VM with one without")
	}

	same := exec.SandboxNameWith("docker:27-dind", "/opt/earth", "/store", "8G",
		[]string{"--feature", "containerd-snapshotter=true"})
	if same != withFlag {
		t.Error("the same command gave two names")
	}
}
