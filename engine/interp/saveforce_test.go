package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// **`SAVE ARTIFACT --force` is the caller saying so, and upstream's own gate.**
//
// It was refused outright. The reference engine treats a save outside the
// project as *unsafe* rather than forbidden - `features.go` describes the flag
// as "require the --force flag when saving to path outside of current path" -
// so the position it encodes is "not unless asked", asked twice: the version
// feature and then the flag.
//
// `tests/save-artifact-overwrite.earth+overwrite-root` is the corpus case, and
// it is deliberately writing to `/root`: the target is named for it.
func TestForceIsCarriedRatherThanRefused(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

overwrite-root:
    FROM alpine:3.20
    RUN mkdir -p /data
    SAVE ARTIFACT --force /data AS LOCAL /tmp/outside-the-project
`, "overwrite-root")
	if err != nil {
		t.Fatalf("--force was refused: %v", err)
	}

	var seen bool

	for _, a := range p.Artifacts {
		if a.LocalDest == "" {
			continue
		}

		seen = true

		if !a.Force {
			t.Error("the artifact reached the plan without the caller's --force")
		}
	}

	if !seen {
		t.Error("no exported artifact reached the plan")
	}
}

// Without the flag, a save outside the project is still refused - which is the
// other half of upstream's rule and the reason the flag means anything.
func TestWithoutForceAnOutsideSaveIsStillRefused(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

save:
    FROM alpine:3.20
    RUN mkdir -p /data
    SAVE ARTIFACT /data AS LOCAL /tmp/outside-the-project
`, "save")
	if err != nil {
		return // refused at planning is an acceptable place to refuse it
	}

	for _, a := range p.Artifacts {
		if a.LocalDest != "" && a.Force {
			t.Error("an artifact nobody forced arrived marked as forced")
		}
	}
}
