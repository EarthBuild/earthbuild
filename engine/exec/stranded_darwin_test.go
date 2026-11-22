//go:build darwin

package exec_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// inspectOf builds what `container inspect` answers for a VM bind-mounting the
// given host directories, so a test can describe a sandbox by the only property
// the reaper cares about.
func inspectOf(t *testing.T, sources ...string) []byte {
	t.Helper()

	mounts := make([]any, 0, 1+len(sources))
	mounts = append(mounts,
		// The VM's own storage. A volume, not a bind mount: its source is a
		// disk image the backend owns, and it exists precisely as long as the
		// VM does - so it can never be evidence about the VM's usefulness.
		map[string]any{
			"destination": "/var/lib/earthbuild/fast",
			"source":      "/nowhere/volumes/earthbuild-x-fast/volume.img",
			"type":        map[string]any{"volume": map[string]any{"name": "earthbuild-x-fast"}},
		})

	for _, src := range sources {
		mounts = append(mounts, map[string]any{
			"destination": "/earth",
			"source":      src,
			"type":        map[string]any{"virtiofs": map[string]any{}},
		})
	}

	out, err := json.Marshal([]any{map[string]any{
		"configuration": map[string]any{"id": "earthbuild-x", "mounts": mounts},
		"status":        "stopped",
	}})
	if err != nil {
		t.Fatalf("build the inspect fixture: %v", err)
	}

	return out
}

// TestASandboxNamedForAVanishedDirectoryIsStranded is the reap rule.
//
// A content-named VM has no owning process, so the old "is the pid gone?"
// question cannot be asked of it - and 32 of them accumulated on the
// development machine, each holding a volume and a gigabyte, until the system
// file table overflowed. The name is a digest over the directories it mounts,
// so a VM whose directories have gone can never be named again by anything.
// That is what makes removing it safe rather than merely tidy.
func TestASandboxNamedForAVanishedDirectoryIsStranded(t *testing.T) {
	t.Parallel()

	gone := filepath.Join(t.TempDir(), "removed-since")

	if !exec.SandboxIsStranded(inspectOf(t, gone)) {
		t.Fatalf("a VM mounting %s, which does not exist, is not reachable and should be stranded", gone)
	}
}

func TestASandboxWhoseDirectoriesRemainIsNotStranded(t *testing.T) {
	t.Parallel()

	here := t.TempDir()

	if exec.SandboxIsStranded(inspectOf(t, here)) {
		t.Fatalf("a VM mounting %s, which exists, is still reachable and must be kept", here)
	}
}

// TestAVolumeIsNotEvidenceThatASandboxIsStranded guards the one mount every
// sandbox has. Counting it would strand every VM on the machine, because the
// disk image behind a volume is not a path this engine ever created.
func TestAVolumeIsNotEvidenceThatASandboxIsStranded(t *testing.T) {
	t.Parallel()

	if exec.SandboxIsStranded(inspectOf(t)) {
		t.Fatal("a VM with only its own volume has nothing to be stranded by")
	}
}

// TestAnUnreadableInspectStrandsNothing: the decision this feeds is a forced
// removal, so an answer that cannot be read has to mean keep. Reaping on a
// garbled listing takes a sandbox out from under a concurrent build.
func TestAnUnreadableInspectStrandsNothing(t *testing.T) {
	t.Parallel()

	for _, out := range [][]byte{nil, []byte(""), []byte("not json"), []byte("[]")} {
		if exec.SandboxIsStranded(out) {
			t.Fatalf("%q is not evidence of anything and must not strand a VM", out)
		}
	}
}

// inspectMany builds what `container inspect a b c` answers, so the batch path
// can be tested without a backend. One call answers for every VM on the machine;
// asking per VM cost more than the boot the reap exists to make cheaper.
func inspectMany(t *testing.T, byName map[string][]string) []byte {
	t.Helper()

	names := sortedKeys(byName)
	docs := make([]any, 0, len(names))

	for _, name := range names {
		mounts := make([]any, 0, len(byName[name]))

		for _, src := range byName[name] {
			mounts = append(mounts, map[string]any{
				"source": src,
				"type":   map[string]any{"virtiofs": map[string]any{}},
			})
		}

		docs = append(docs, map[string]any{
			"configuration": map[string]any{"id": name, "mounts": mounts},
		})
	}

	out, err := json.Marshal(docs)
	if err != nil {
		t.Fatalf("build the inspect fixture: %v", err)
	}

	return out
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// TestOnlyTheSandboxesWhoseDirectoriesWentAreNamed: the reap removes by name,
// so a batch inspection that could not tell which VM a missing directory
// belonged to would take the wrong one away.
func TestOnlyTheSandboxesWhoseDirectoriesWentAreNamed(t *testing.T) {
	t.Parallel()

	here := t.TempDir()
	gone := filepath.Join(here, "removed-since")

	out := inspectMany(t, map[string][]string{
		"earthbuild-keep":   {here},
		"earthbuild-strand": {gone},
		"earthbuild-both":   {here, gone},
	})

	got := exec.StrandedSandboxes(out)

	want := []string{"earthbuild-both", "earthbuild-strand"}
	if !slices.Equal(got, want) {
		t.Fatalf("stranded %v, want %v", got, want)
	}
}

// TestASandboxWithNoIdentityIsNeverReaped: the removal is `container rm -f`, so
// a document this cannot attribute must yield no name at all rather than a
// plausible one.
func TestASandboxWithNoIdentityIsNeverReaped(t *testing.T) {
	t.Parallel()

	out := inspectMany(t, map[string][]string{"": {filepath.Join(t.TempDir(), "gone")}})

	if got := exec.StrandedSandboxes(out); len(got) != 0 {
		t.Fatalf("named %v from a document with no id", got)
	}
}
