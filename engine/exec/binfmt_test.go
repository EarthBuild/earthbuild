package exec

import (
	"os"
	"path/filepath"
	"testing"
)

func binfmtDir(t *testing.T, entries map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	for name, body := range entries {
		err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

// **What the machine can emulate is what binfmt says it can.** An entry names
// an interpreter and says whether it is enabled; a disabled one is registered
// and will not run, which is not the same as absent and must not be reported as
// available.
func TestOnlyEnabledInterpretersAreOffered(t *testing.T) {
	t.Parallel()

	dir := binfmtDir(t, map[string]string{
		"qemu-aarch64": "enabled\ninterpreter /usr/bin/qemu-aarch64-static\n",
		"qemu-riscv64": "disabled\ninterpreter /usr/bin/qemu-riscv64-static\n",
		"status":       "enabled\n",
	})

	got := emulatedPlatforms(dir)

	if len(got) != 1 {
		t.Fatalf("got %v, want one platform", got)
	}

	if got[0].OS != "linux" || got[0].Arch != archARM64 {
		t.Errorf("got %v, want linux/arm64", got[0])
	}
}

// The register's own control file is not an interpreter, and neither is
// anything whose name this does not recognise: reporting an architecture the
// engine then cannot name would place a step nothing can run.
func TestUnrecognisedEntriesAreIgnored(t *testing.T) {
	t.Parallel()

	dir := binfmtDir(t, map[string]string{
		"status":     "enabled\n",
		"register":   "",
		"jarwrapper": "enabled\ninterpreter /usr/bin/jexec\n",
	})

	if got := emulatedPlatforms(dir); len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

// A machine with no binfmt at all is the ordinary case and is not an error: it
// emulates nothing, and every build that does not need emulation is unaffected.
func TestNoRegisterIsNotAnError(t *testing.T) {
	t.Parallel()

	if got := emulatedPlatforms(filepath.Join(t.TempDir(), "absent")); len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

// Deterministic, because it reaches a Worker and a Worker reaches placement:
// two runs of one build must consider the same machines in the same order.
func TestTheListIsSorted(t *testing.T) {
	t.Parallel()

	dir := binfmtDir(t, map[string]string{
		"qemu-s390x":   "enabled\n",
		"qemu-aarch64": "enabled\n",
		"qemu-ppc64le": "enabled\n",
	})

	got := emulatedPlatforms(dir)
	for i := 1; i < len(got); i++ {
		if got[i-1].Arch > got[i].Arch {
			t.Fatalf("not sorted: %v", got)
		}
	}
}
