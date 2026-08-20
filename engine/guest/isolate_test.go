package guest_test

import (
	"context"
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// TestUnisolatedStepsAreRefused is the decision this whole piece turns on.
//
// A guest that cannot confine a step must refuse it, not run it anyway. An
// unconfined step produces a result that *looks* cacheable and is not, because
// ε no longer bounds what it observed (green paper A3) - so the build appears
// to have succeeded and the cache is now wrong. Refusing is strictly better.
func TestUnisolatedStepsAreRefused(t *testing.T) {
	t.Parallel()

	if !guest.NeedsIsolation(t) {
		return
	}

	if os.Geteuid() == 0 {
		t.Skip("running as root, so isolation is available")
	}

	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: stepRoot(t)}})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	_, _, err = c.Exec(context.Background(), h, []string{testTrue}, nil)
	if err == nil {
		t.Fatal("a step ran unconfined; it must be refused")
	}

	if !strings.Contains(err.Error(), "isolate") {
		t.Errorf("refusal does not explain itself: %v", err)
	}
}

// TestConfinementCanBeWaivedExplicitly: tests that are not making claims about
// caching may opt out, but only by saying so.
func TestConfinementCanBeWaivedExplicitly(t *testing.T) {
	t.Parallel()

	if !guest.NeedsIsolation(t) {
		return
	}

	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: stepRoot(t)}, Unconfined: true})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	_, _, err = c.Exec(context.Background(), h, []string{testTrue}, nil)
	if err != nil {
		t.Fatalf("an explicitly unconfined step was refused: %v", err)
	}
}

// TestChrootHidesTheHost runs where confinement is actually possible. It is the
// property that makes ε bounded - a step cannot name a path outside its own
// filesystem, so it cannot observe anything the key does not cover.
//
// Gated on `NeedsIsolation`, which mounts what a step mounts, and **not** on
// `Geteuid() == 0`, which is what it used to ask. `CanIsolate`'s own doc
// comment rejects that check by name - *"rather than `Getuid() == 0`, which
// would refuse a machine that grants CAP_SYS_ADMIN to an unprivileged user"* -
// so the package had the right probe, documented, with a test of its own, and
// this test asked the wrong question two files away.
//
// It is wrong in both directions, and both were live. In a build container euid
// is 0 and mounts are refused, so the test ran and failed - red in `earth
// +unit-test --pkgname=./engine/...` and in every container since. On an
// ordinary Linux developer machine euid is not 0, so it skipped and had
// therefore never run at all; through `nstest` it now does.
func TestChrootHidesTheHost(t *testing.T) {
	if !guest.NeedsIsolation(t) {
		return
	}

	root := t.TempDir()

	// A witness outside the root that the step must not be able to reach.
	outside := filepath.Join(t.TempDir(), "host-secret")
	err := os.WriteFile(outside, []byte("visible"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// A dynamically linked probe cannot run in an empty root: the loader it
	// names is not there, and `fork/exec` reports ENOENT for the *interpreter*
	// while pointing at the program, which is the least helpful error in Unix.
	//
	// Skipped rather than worked around, because copying an interpreter and
	// whatever it in turn needs is a small package manager, and this test is
	// about chroot. It runs wherever the test binary is static - which is how
	// this repository builds its own (CGO_ENABLED=0) - and says so otherwise.
	//
	// Only visible once the gate below was corrected: with `Geteuid() == 0` this
	// test never ran on a developer machine at all.
	if interp := interpreterOf(t, "/proc/self/exe"); interp != "" {
		t.Skipf("this test binary is dynamically linked (%s), so it cannot run"+
			" inside an empty root; build it with CGO_ENABLED=0 to exercise this", interp)
	}

	// The probe is this test binary, copied inside so it exists after chroot.
	self, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Skip("cannot read own binary")
	}

	probe := filepath.Join(root, "probe")
	err = os.WriteFile(probe, self, 0o755) //nolint:gosec // a test binary
	if err != nil {
		t.Fatal(err)
	}

	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	code, out, err := c.Exec(context.Background(), h,
		[]string{"/probe", "-test.run", "TestProbeCannotSeeHost", "-test.v"},
		[]string{"EARTH_PROBE_PATH=" + outside, "EARTH_PROBE=1"})
	if err != nil {
		t.Fatalf("confined exec failed: %v", err)
	}

	if code != 0 {
		t.Errorf("probe reported the host was visible from inside the chroot:\n%s", out)
	}

	// Guard against a vacuous pass. A probe that skipped - because it was not
	// re-executed, or because the environment did not reach it - also exits
	// zero, and would report confinement that was never tested.
	if !strings.Contains(out, "PASS: TestProbeCannotSeeHost") {
		t.Errorf("the probe did not actually run; this proves nothing:\n%s", out)
	}

	if strings.Contains(out, "SKIP") {
		t.Errorf("the probe skipped rather than checking:\n%s", out)
	}
}

// TestProbeCannotSeeHost runs *inside* the chroot, re-executed from the copied
// binary. It asserts that a path the guest can see is unreachable from the step.
func TestProbeCannotSeeHost(t *testing.T) {
	t.Parallel()

	if os.Getenv("EARTH_PROBE") == "" {
		t.Skip("not the probe")
	}

	p := os.Getenv("EARTH_PROBE_PATH")
	if p == "" {
		t.Fatal("probe given no path")
	}

	_, err := os.Stat(p)
	if err == nil {
		t.Fatalf("%s is reachable from inside the chroot; the step is not confined", p)
	}
}

// interpreterOf is the ELF interpreter a binary needs, or "" if it needs none.
//
// `debug/elf` from the standard library rather than shelling out to `file` or
// `ldd`: the question is one program header, and `ldd` on an untrusted binary
// runs it.
func interpreterOf(t *testing.T, path string) string {
	t.Helper()

	f, err := elf.Open(path)
	if err != nil {
		// Not an ELF file, or unreadable. Neither is this test's question, and
		// answering "no interpreter" lets the exec below report what it finds.
		return ""
	}

	defer func() { _ = f.Close() }()

	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}

		b := make([]byte, p.Filesz)
		if _, err := p.ReadAt(b, 0); err != nil {
			return "an interpreter this test could not read"
		}

		return strings.TrimRight(string(b), "\x00")
	}

	return ""
}
