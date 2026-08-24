package guest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Limits bound what a step may consume.
//
// Unlike isolation, these are not a correctness property: a step with no memory
// bound still produces a *correct* result, it just might take the machine down
// with it. So an unavailable cgroup is a degradation to warn about, never a
// refusal - refusing there would over-apply the rule that protects ε.
type Limits struct {
	// MemoryMax is the memory ceiling in bytes. Zero means unbounded.
	MemoryMax int64
	// PidsMax caps the process count, which is what stops a fork bomb. Zero
	// means unbounded.
	PidsMax int64
	// CPUMax is microseconds of CPU per CPUPeriod. Zero means unbounded.
	CPUMax, CPUPeriod int64
}

// Empty reports whether any limit is set.
func (l Limits) Empty() bool {
	return l.MemoryMax == 0 && l.PidsMax == 0 && l.CPUMax == 0
}

const cgroupRoot = "/sys/fs/cgroup"

// cgroup is a created control group, and the file descriptor a child is cloned
// directly into.
type cgroup struct {
	path string
	fd   int
}

// newCgroup creates a control group for one step and applies the limits.
//
// Returns a nil cgroup and no error when cgroups are unavailable: the step then
// runs unbounded, which is worse than bounded and much better than not running.
func newCgroup(name string, l Limits) (*cgroup, error) {
	if l.Empty() {
		return nil, nil //nolint:nilnil // no limits requested, nothing to create
	}

	_, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers"))
	if err != nil {
		return degraded("cgroup v2 is not mounted")
	}

	// cgroup v2 requires a controller to be enabled in the *parent's*
	// subtree_control before a child may use it. Without this the child
	// directory is created, memory.max is written, and nothing is enforced -
	// which is precisely the failure this code shipped with until a test that
	// allocated 256 MiB under a 16 MiB ceiling went unpunished.
	// Where this process may actually create one, which is not always the root
	// (E124): rootless, that is the delegated subtree the engine was started
	// inside, and there is none at all outside a delegated scope.
	base, ok := cgroupParent()
	if !ok {
		return degraded("no cgroup directory this process may write" +
			" (rootless needs the engine to be started inside a delegated scope," +
			" as `systemd-run --user --scope` gives)")
	}

	// The guest is inside the cgroup the host left it in, so that cgroup holds
	// a process and will not enable controllers for its children - the same
	// "no internal processes" rule, one level down (E124). Best effort: as
	// root there is nothing to step out of.
	_ = stepAsideSelf(base)

	parent := filepath.Join(base, "earthbuild")

	err = os.MkdirAll(parent, 0o750)
	if err != nil {
		return degraded("cannot create the parent cgroup: " + err.Error())
	}

	for _, dir := range []string{base, parent} {
		if err := os.WriteFile(filepath.Join(dir, "cgroup.subtree_control"),
			[]byte("+memory +pids +cpu"), 0o644); err != nil {
			// Not fatal on the root: a delegated subtree often has the
			// controllers enabled already and refuses the write.
			continue
		}
	}

	path := filepath.Join(parent, name)
	err = os.MkdirAll(path, 0o750)
	if err != nil {
		return degraded("cannot create the step cgroup: " + err.Error())
	}

	// Best effort: a controller that cannot be written is one limit not
	// applied, not a reason to abandon the others.
	if l.MemoryMax > 0 {
		writeLimit(path, "memory.max", strconv.FormatInt(l.MemoryMax, 10))

		// memory.max bounds resident memory only. With swap left at its default
		// of "max", a step allocating far past the ceiling is simply paged out
		// and survives - measured on a host with 238 GiB of swap, where a 256 MiB
		// allocation under a 16 MiB ceiling completed without being stopped.
		//
		// Bounding swap to zero is what makes MemoryMax a ceiling rather than a
		// hint. It also matches what a build wants: a step thrashing swap is
		// slower than a step that died and told you so.
		writeLimit(path, "memory.swap.max", "0")
	}

	if l.PidsMax > 0 {
		writeLimit(path, "pids.max", strconv.FormatInt(l.PidsMax, 10))
	}

	if l.CPUMax > 0 {
		period := l.CPUPeriod
		if period == 0 {
			period = 100000
		}

		writeLimit(path, "cpu.max", fmt.Sprintf("%d %d", l.CPUMax, period))
	}

	// Verify the limit took. Writing memory.max into a cgroup whose parent has
	// not enabled the controller succeeds and does nothing, so the write is not
	// evidence - reading it back is.
	if l.MemoryMax > 0 {
		//nolint:gosec // a cgroup path this process made
		if got, readErr := os.ReadFile(filepath.Join(path, "memory.max")); readErr != nil ||
			strings.TrimSpace(string(got)) != strconv.FormatInt(l.MemoryMax, 10) {
			_ = os.Remove(path)

			return degraded("the memory controller is not delegated to this cgroup")
		}
	}

	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		_ = os.Remove(path)

		return degraded("cannot open the cgroup for CLONE_INTO_CGROUP: " + err.Error())
	}

	return &cgroup{path: path, fd: fd}, nil
}

func writeLimit(dir, file, value string) {
	// Errors are deliberately ignored: see newCgroup's comment. A limit that
	// cannot be set is reported by the limit not taking effect, and the caller
	// has no better response than to continue.
	_ = os.WriteFile(filepath.Join(dir, file), []byte(value), 0o644) //nolint:gosec // cgroup interface files
}

// apply makes the child start *inside* the cgroup.
//
// This uses CLONE_INTO_CGROUP rather than writing the pid to cgroup.procs after
// the fork, and the difference is not cosmetic: a process added afterwards runs
// unconstrained between exec and enrolment, so a step that allocates
// immediately can exceed its memory ceiling before the ceiling exists. Cloning
// into the cgroup closes that window entirely.
func (c *cgroup) apply(attr *syscall.SysProcAttr) {
	if c == nil {
		return
	}

	attr.UseCgroupFD = true
	attr.CgroupFD = c.fd
}

// remove tears the cgroup down. A cgroup outlives its step if this is missed,
// and thousands of them make a machine unhappy in ways that are hard to trace
// back here.
func (c *cgroup) remove() error {
	if c == nil {
		return nil
	}

	err := syscall.Close(c.fd)
	if err != nil && !errors.Is(err, syscall.EBADF) {
		return fmt.Errorf("close cgroup fd: %w", err)
	}

	err = os.Remove(c.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove cgroup: %w", err)
	}

	return nil
}
