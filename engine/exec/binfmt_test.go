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

// The names `tonistiigi/binfmt` registers are read too.
//
// **The engine's own message recommends that tool**, and the tool registers by
// architecture name - `x86_64`, `arm`, `riscv64` - rather than by the
// `qemu-`-prefixed interpreter name the map knew. So following this engine's
// advice installed emulation it then reported as absent, and the second refusal
// said the same words as the first (E959).
//
// Observed on Docker Desktop's VM after `--install all`: `arm`, `i386`,
// `mips64`, `mips64le`, `ppc64le`, `riscv64`, `s390x`, `x86_64`, alongside
// `qemu-arm` and a few other prefixed duplicates.
func TestBothSpellingsOfAnInterpreterAreRead(t *testing.T) {
	t.Parallel()

	dir := binfmtDir(t, map[string]string{
		"x86_64":       "enabled\ninterpreter /usr/bin/qemu-x86_64\n",
		"qemu-aarch64": "enabled\ninterpreter /usr/bin/qemu-aarch64\n",
		"arm":          "enabled\ninterpreter /usr/bin/qemu-arm\n",
		"i386":         "enabled\ninterpreter /usr/bin/qemu-i386\n",
		// Registered and off: still not available, whichever way it is spelled.
		"riscv64": "disabled\ninterpreter /usr/bin/qemu-riscv64\n",
		// Not an interpreter this engine knows, and not a reason to guess.
		"jarwrapper": "enabled\ninterpreter /usr/bin/jexec\n",
	})

	offered := emulatedPlatforms(dir)

	got := make([]string, 0, len(offered))
	for _, p := range offered {
		got = append(got, p.String())
	}

	want := map[string]bool{"linux/amd64": true, "linux/arm64": true, "linux/arm": true, "linux/386": true}

	for _, g := range got {
		if !want[g] {
			t.Errorf("offered %s, which nothing here registers", g)
		}

		delete(want, g)
	}

	for missing := range want {
		t.Errorf("%s is registered and enabled and was not offered", missing)
	}
}
