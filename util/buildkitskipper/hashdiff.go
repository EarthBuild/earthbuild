package buildkitskipper

import "fmt"

// HashLogDiff describes what changed in a target's inputs between two runs.
type HashLogDiff struct {
	// Added contains inputs present in the current run but not the previous one.
	Added []HashInputRecord
	// Removed contains inputs present in the previous run but not the current one.
	Removed []HashInputRecord
	// Changed contains inputs whose Detail value changed for the same Label+position.
	Changed []HashInputChange
}

// HashInputChange describes a single input whose value changed between runs.
type HashInputChange struct {
	Label  string
	Before string
	After  string
}

// IsEmpty returns true when there are no differences.
func (d HashLogDiff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// Lines returns a human-readable slice of diff lines, one per change.
func (d HashLogDiff) Lines() []string {
	lines := make([]string, 0, len(d.Added)+len(d.Removed)+len(d.Changed))

	for _, c := range d.Changed {
		lines = append(lines, fmt.Sprintf("~ %-16s %s → %s", c.Label, c.Before, c.After))
	}

	for _, r := range d.Removed {
		lines = append(lines, fmt.Sprintf("- %-16s %s", r.Label, r.Detail))
	}

	for _, a := range d.Added {
		lines = append(lines, fmt.Sprintf("+ %-16s %s", a.Label, a.Detail))
	}

	return lines
}

// DiffHashLog computes the difference between prev and current hash logs.
// It matches entries positionally within the same Label group: entries with the
// same label are compared in order, and value changes within that group are
// reported as Changed rather than Add+Remove pairs.
func DiffHashLog(prev, current []HashInputRecord) HashLogDiff {
	var diff HashLogDiff

	// Group entries by label, preserving order within each group.
	prevByLabel := groupByLabel(prev)
	currByLabel := groupByLabel(current)

	// Collect all labels seen in either run, in the order they first appear.
	seen := make(map[string]struct{})
	labels := make([]string, 0, len(prevByLabel)+len(currByLabel))

	for _, r := range prev {
		if _, ok := seen[r.Label]; !ok {
			seen[r.Label] = struct{}{}
			labels = append(labels, r.Label)
		}
	}

	for _, r := range current {
		if _, ok := seen[r.Label]; !ok {
			seen[r.Label] = struct{}{}
			labels = append(labels, r.Label)
		}
	}

	for _, label := range labels {
		prevGroup := prevByLabel[label]
		currGroup := currByLabel[label]

		// Pair entries positionally within the group.
		minLen := min(len(prevGroup), len(currGroup))

		for i := range minLen {
			if prevGroup[i].Detail != currGroup[i].Detail {
				diff.Changed = append(diff.Changed, HashInputChange{
					Label:  label,
					Before: prevGroup[i].Detail,
					After:  currGroup[i].Detail,
				})
			}
		}

		// Extra entries in prev = removed.
		for i := minLen; i < len(prevGroup); i++ {
			diff.Removed = append(diff.Removed, prevGroup[i])
		}

		// Extra entries in curr = added.
		for i := minLen; i < len(currGroup); i++ {
			diff.Added = append(diff.Added, currGroup[i])
		}
	}

	return diff
}

func groupByLabel(records []HashInputRecord) map[string][]HashInputRecord {
	m := make(map[string][]HashInputRecord)

	for _, r := range records {
		m[r.Label] = append(m[r.Label], r)
	}

	return m
}
