package buildkitskipper_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/util/buildkitskipper"
	"github.com/stretchr/testify/require"
)

const (
	labelARG  = "ARG"
	labelRUN  = "RUN"
	detailFoo = "FOO=bar"
)

func TestDiffHashLog_NoDiff(t *testing.T) {
	t.Parallel()

	prev := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: "GO_VERSION=1.21"},
		{Label: labelRUN, Detail: "go build ./..."},
	}
	diff := buildkitskipper.DiffHashLog(prev, prev)
	require.True(t, diff.IsEmpty())
	require.Empty(t, diff.Lines())
}

func TestDiffHashLog_ChangedValue(t *testing.T) {
	t.Parallel()

	prev := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: "GO_VERSION=1.21"},
	}
	curr := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: "GO_VERSION=1.22"},
	}
	diff := buildkitskipper.DiffHashLog(prev, curr)
	require.False(t, diff.IsEmpty())
	require.Len(t, diff.Changed, 1)
	require.Equal(t, labelARG, diff.Changed[0].Label)
	require.Equal(t, "GO_VERSION=1.21", diff.Changed[0].Before)
	require.Equal(t, "GO_VERSION=1.22", diff.Changed[0].After)

	lines := diff.Lines()
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "~")
	require.Contains(t, lines[0], "GO_VERSION=1.21")
	require.Contains(t, lines[0], "GO_VERSION=1.22")
}

func TestDiffHashLog_AddedEntry(t *testing.T) {
	t.Parallel()

	prev := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: detailFoo},
	}
	curr := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: detailFoo},
		{Label: labelRUN, Detail: "echo hello"},
	}
	diff := buildkitskipper.DiffHashLog(prev, curr)
	require.False(t, diff.IsEmpty())
	require.Len(t, diff.Added, 1)
	require.Equal(t, labelRUN, diff.Added[0].Label)
	require.Equal(t, "echo hello", diff.Added[0].Detail)

	lines := diff.Lines()
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "+")
	require.Contains(t, lines[0], "echo hello")
}

func TestDiffHashLog_RemovedEntry(t *testing.T) {
	t.Parallel()

	prev := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: detailFoo},
		{Label: labelRUN, Detail: "echo old"},
	}
	curr := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: detailFoo},
	}
	diff := buildkitskipper.DiffHashLog(prev, curr)
	require.False(t, diff.IsEmpty())
	require.Len(t, diff.Removed, 1)
	require.Equal(t, labelRUN, diff.Removed[0].Label)

	lines := diff.Lines()
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "-")
	require.Contains(t, lines[0], "echo old")
}

func TestDiffHashLog_MultipleChanges(t *testing.T) {
	t.Parallel()

	prev := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: "A=1"},
		{Label: labelARG, Detail: "B=2"},
		{Label: labelRUN, Detail: "echo old"},
	}
	curr := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: "A=1"},
		{Label: labelARG, Detail: "B=99"},
		{Label: labelRUN, Detail: "echo new"},
		{Label: labelRUN, Detail: "echo added"},
	}
	diff := buildkitskipper.DiffHashLog(prev, curr)
	require.False(t, diff.IsEmpty())
	require.Len(t, diff.Changed, 2)
	require.Empty(t, diff.Removed)
	require.Len(t, diff.Added, 1)
}

func TestDiffHashLog_EmptyPrev(t *testing.T) {
	t.Parallel()

	curr := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: detailFoo},
	}
	diff := buildkitskipper.DiffHashLog(nil, curr)
	require.False(t, diff.IsEmpty())
	require.Len(t, diff.Added, 1)
}

func TestDiffHashLog_EmptyCurr(t *testing.T) {
	t.Parallel()

	prev := []buildkitskipper.HashInputRecord{
		{Label: labelARG, Detail: detailFoo},
	}
	diff := buildkitskipper.DiffHashLog(prev, nil)
	require.False(t, diff.IsEmpty())
	require.Len(t, diff.Removed, 1)
}
