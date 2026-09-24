package core

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// maxExamples is how many differing paths a report shows before summarising.
//
// Four thousand changed files is not a diagnostic. A handful, ranked, with a
// count for the rest, is.
const maxExamples = 5

// Report renders a divergence for a human, green paper B.4 and B.5.
//
// "Step +build missed cache" is a location, not a diagnosis. The report has to
// descend to the files, rank them so the interesting one is visible, and name
// the pathological cases rather than leaving the reader to spot them.
func Report(d Divergence) string {
	if d.Cause == CauseNone {
		return "identical\n"
	}

	var b strings.Builder

	where := d.Meta.Description
	if where == "" {
		where = d.Step.String()[:12]
	}

	fmt.Fprintf(&b, "%s  %s\n", where, d.Cause)

	if d.Cause == CauseNonDeterminism {
		fmt.Fprintf(&b, "  every component of the key is identical; the step is not reproducible\n")
	}

	if changed := changedPaths(d.A, d.B); len(changed) > 0 {
		total := len(changed)
		shown := changed

		if len(shown) > maxExamples {
			shown = shown[:maxExamples]
		}

		fmt.Fprintf(&b, "  keyed on %d inputs; %d changed:\n", len(d.A.Observation.Reads), total)

		for _, c := range shown {
			note := ""
			if s := suspicion(c.path); s != "" {
				note = "   <- " + s
			}

			fmt.Fprintf(&b, "    %-28s %s%s\n", c.path, c.how, note)
		}

		if total > len(shown) {
			fmt.Fprintf(&b, "    ... and %d more\n", total-len(shown))
		}
	}

	if d.Meta.Source != "" {
		fmt.Fprintf(&b, "  at %s\n", d.Meta.Source)
	}

	return b.String()
}

// change is one difference between two steps' observations.
//
// Constructed with field names. `path` and `how` are both strings and adjacent,
// so a positional literal would survive them being swapped and quietly report a
// description where a path belongs - the compiler has nothing to say about
// `change{a, b, …}` when a and b are the same type (E187).
type change struct {
	path string
	how  string
	rank int
}

// changedPaths compares two steps' observations and returns what differs,
// ranked so that the line worth reading is near the top.
func changedPaths(a, b StepRecord) []change {
	if !a.Observed || !b.Observed {
		return nil
	}

	var out []change

	for p, da := range a.Observation.Reads {
		db, ok := b.Observation.Reads[p]

		switch {
		case !ok:
			out = append(out, change{path: p, how: "no longer read", rank: rank(p)})
		case da != db:
			out = append(out, change{path: p, how: "contents differ", rank: rank(p)})
		}
	}

	for p := range b.Observation.Reads {
		if _, ok := a.Observation.Reads[p]; !ok {
			out = append(out, change{path: p, how: "newly read", rank: rank(p)})
		}
	}

	// Ranked, then alphabetical, so a report is reproducible rather than
	// depending on map order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank > out[j].rank
		}

		return out[i].path < out[j].path
	})

	return out
}

// rank orders changed paths by how likely they are to be the actual bug.
//
// A dependency on a `.git` directory or an editor swap file is almost always
// unintended, and is exactly the line that saves someone an afternoon - so it
// is promoted above the source file they expected to see.
func rank(p string) int {
	if suspicion(p) != "" {
		return 2
	}

	// Generated and vendored trees are usually noise.
	for _, dir := range []string{"vendor/", "node_modules/", "target/", ".cache/"} {
		if strings.Contains(p, dir) {
			return 0
		}
	}

	return 1
}

// suspicion names a path that a step almost certainly did not mean to depend
// on. Returning the reason rather than a boolean lets the report say why.
func suspicion(p string) string {
	base := path.Base(p)

	switch {
	case strings.Contains(p, "/.git/"), strings.HasPrefix(p, ".git/"), base == ".git":
		return "a step depending on git state is usually unintended"
	case strings.HasSuffix(base, ".swp"), strings.HasSuffix(base, "~"):
		return "editor scratch file"
	case base == ".DS_Store":
		return "filesystem noise"
	case strings.Contains(p, "/tmp/"), strings.HasPrefix(p, "tmp/"),
		strings.HasPrefix(p, "/proc/"), strings.HasPrefix(p, "/sys/"):
		return "ephemeral path; the result will not reproduce"
	}

	return ""
}

// Counterfactual reports how many steps that missed under the chain key would
// have hit had they been keyed on what they actually read.
//
// It is a diagnostic and a measurement at once: run it across a corpus and it
// quantifies what observed-input caching is worth *before* the feature is
// switched on. If the number is small, that is an argument against building it,
// which is the point of measuring rather than assuming.
func Counterfactual(prev, cur *Record) (wouldHaveHit, missed int) {
	for _, c := range cur.Steps {
		if c.Outcome != OutcomeMiss || !c.Observed {
			continue
		}

		missed++

		p, ok := prev.find(c.Ident)
		if !ok || !p.Observed {
			continue
		}

		// Nothing the step read changed, yet the chain key missed: the base
		// moved underneath a step that could not observe it.
		if len(changedPaths(p, c)) == 0 && p.Layer != c.Layer {
			wouldHaveHit++
		}
	}

	return wouldHaveHit, missed
}

// observationDigest summarises an observation for the record, so that two
// records can be compared without carrying every path twice.
func observationDigest(obs Observation) ir.NodeID {
	h := ir.NewHasher()

	h.Byte(domainComponent)

	paths := make([]string, 0, len(obs.Reads))
	for p := range obs.Reads {
		paths = append(paths, p)
	}

	sort.Strings(paths)
	h.Count(len(paths))

	for _, p := range paths {
		h.Str(p)

		d := obs.Reads[p]
		h.Fixed(d[:])
	}

	return h.Sum()
}
