//go:build darwin

package exec

import (
	"encoding/json"
	"os"
	osexec "os/exec"
	"slices"
	"strings"
)

// A sandbox is named for the directories it mounts, so a sandbox whose
// directories have gone can never be named again - by this build or any other.
// Nothing will ever ask for it, nothing will ever resume it, and it holds a
// volume, a gigabyte of disk and, while running, tens of thousands of open
// descriptors on the layer store.
//
// That is the whole of the reap rule here, and it is the same argument `Remove`
// already makes about taking a volume away with its VM. It matters because the
// *old* rule cannot be applied to these at all: `IsOrphanedSandbox` asks whether
// an owning process has exited, and a content-named VM has no owning process by
// design. So nothing reaped them. Thirty-two accumulated on the development
// machine across a morning of benchmarking - each run used a fresh temporary
// store, so each minted a name that would never recur - until the *system-wide*
// file table overflowed and every command on the machine failed with ENFILE.
//
// The reap is deliberately conservative in both directions it can be wrong:
// an inspection that cannot be read strands nothing, and a mount whose source
// still exists keeps the VM even if this engine would never choose that name
// again. Removing a live sandbox out from under a concurrent build in another
// project is the failure this must not have.

// strandedLimit is how many VMs one inspection asks about. `container inspect`
// takes many ids, but a machine holding hundreds should not spend a build's
// first second on tidying; the rest are found on the next run.
const strandedLimit = 64

// inspected is the part of `container inspect` this reads. Everything else in
// that document is ignored on purpose: the fields are the backend's and change
// between releases, and a decoder that insisted on all of them would fail shut
// the first time one moved.
type inspected struct {
	Configuration struct {
		ID     string `json:"id"`
		Mounts []struct {
			Source string `json:"source"`
			Type   struct {
				Virtiofs *struct{} `json:"virtiofs"`
			} `json:"type"`
		} `json:"mounts"`
	} `json:"configuration"`
}

// StrandedSandboxes names the VMs in an inspection that nothing can name again,
// because a host directory they were named for is gone.
//
// Only virtiofs bind mounts count. Every sandbox also mounts its own volume,
// whose "source" is a disk image the backend created and owns - counting that
// would strand every VM on the machine the first time the path moved.
//
// A document with no id yields no name. The decision this feeds is a forced
// removal, and attributing a missing directory to the wrong VM takes a live
// sandbox out from under a concurrent build.
func StrandedSandboxes(out []byte) []string {
	var found []inspected

	err := json.Unmarshal(out, &found)
	if err != nil {
		return nil
	}

	var stranded []string

	for _, c := range found {
		if c.Configuration.ID == "" || !mountsAreGone(c) {
			continue
		}

		stranded = append(stranded, c.Configuration.ID)
	}

	return stranded
}

// mountsAreGone reports whether any host directory this VM was named for has
// been removed.
func mountsAreGone(c inspected) bool {
	for _, m := range c.Configuration.Mounts {
		if m.Type.Virtiofs == nil || m.Source == "" {
			continue
		}

		_, err := os.Stat(m.Source)
		if err == nil {
			continue
		}

		// A path that exists but cannot be read - a permission error, a stalled
		// network mount - is not absence, and this must not guess.
		if !os.IsNotExist(err) {
			continue
		}

		return true
	}

	return false
}

// SandboxIsStranded reports whether an inspection describes a VM that can never
// be named again. The single-VM form of StrandedSandboxes, for callers holding
// one document.
func SandboxIsStranded(out []byte) bool {
	return len(StrandedSandboxes(out)) > 0
}

// reapStranded removes content-named VMs whose directories have gone.
//
// Best effort, like reapOrphans: a build must not fail because tidying did not
// work, and the cost of missing one is that it is found next time.
func reapStranded(seen map[string]string) {
	names := make([]string, 0, len(seen))

	for name := range seen {
		// The old pid-named VMs are reapOrphans' business. This engine's own
		// sandbox is named later in Start, and a name it is about to choose
		// still mounts directories that exist - so it is never stranded.
		if !strings.HasPrefix(name, "earthbuild-") || IsOrphanedSandbox(name) {
			continue
		}

		names = append(names, name)

		if len(names) == strandedLimit {
			break
		}
	}

	if len(names) == 0 {
		return
	}

	// Sorted, so the subset a machine over the limit tidies this run is the
	// same subset every run rather than whichever the map iterated first.
	slices.Sort(names)

	// One call for all of them, and deliberately on the critical path. The
	// inspection costs 10ms whatever the population - measured, the same as the
	// listing above - and doing it in the background instead risks the process
	// exiting between `container rm` and `container volume rm`, which leaks the
	// disk while removing the only thing that could have reused it.
	ctx, cancel := briefly()
	argv := append([]string{"inspect"}, names...)
	out, err := osexec.CommandContext(ctx, "container", argv...).Output() //nolint:gosec // fixed argv

	cancel()

	if err != nil {
		return
	}

	for _, name := range StrandedSandboxes(out) {
		rmCtx, rmCancel := briefly()
		_ = osexec.CommandContext(rmCtx, "container", "rm", "-f", name).Run() //nolint:gosec // fixed argv

		rmCancel()

		// The volume goes with the VM, for Remove's reason: nothing will name
		// this one again, so leaving it behind leaks the disk without leaving
		// anything able to reuse it.
		volCtx, volCancel := briefly()
		_ = osexec.CommandContext(volCtx, "container", "volume", "rm", volumeFor(name)).Run() //nolint:gosec // fixed argv

		volCancel()
	}
}
