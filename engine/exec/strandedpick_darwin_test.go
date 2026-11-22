package exec

import (
	"slices"
	"testing"
)

// The sandbox this build is about to use is never a candidate for stranding.
//
// It is in the listing like any other `earthbuild-` VM, so it was being sent to
// `container inspect` on every build - about 40ms, measured, on a build whose
// whole incremental loop is 300ms. Nothing was learned by it: whether *this*
// VM still sees its store is asked directly by `seesStore` a few lines later,
// and answered without a subprocess.
func TestStrandedCandidatesSkipsThisBuildsSandbox(t *testing.T) {
	t.Parallel()

	mine := "earthbuild-800c4e7601b6a845"
	seen := map[string]string{
		mine:                          "running",
		"earthbuild-0000000000000001": "stopped",
	}

	got := strandedCandidates(seen, mine)

	if slices.Contains(got, mine) {
		t.Errorf("this build's own sandbox %q was offered for stranding; got %v", mine, got)
	}

	if !slices.Contains(got, "earthbuild-0000000000000001") {
		t.Errorf("another engine's sandbox should still be a candidate; got %v", got)
	}
}

// With nothing but our own VM present there is no call to make at all, which is
// the common case on a developer's machine and the point of the change.
func TestStrandedCandidatesEmptyWhenOnlyOurs(t *testing.T) {
	t.Parallel()

	mine := "earthbuild-800c4e7601b6a845"

	got := strandedCandidates(map[string]string{mine: "running"}, mine)
	if len(got) != 0 {
		t.Errorf("expected no candidates when only our own sandbox is present, got %v", got)
	}
}

// Sorted, so a machine over the limit tidies the same subset every run.
func TestStrandedCandidatesAreSorted(t *testing.T) {
	t.Parallel()

	seen := map[string]string{
		"earthbuild-cccccccccccccccc": "stopped",
		"earthbuild-aaaaaaaaaaaaaaaa": "stopped",
		"earthbuild-bbbbbbbbbbbbbbbb": "stopped",
	}

	got := strandedCandidates(seen, "")
	if !slices.IsSorted(got) {
		t.Errorf("candidates are not sorted: %v", got)
	}
}
