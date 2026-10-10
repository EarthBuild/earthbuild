package interp_test

import (
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `HEALTHCHECK` says how a running container reports its own health.
//
// It changes nothing about the build: no step runs it, no filesystem holds it.
// It is a fact about the *image*, and an image that declares one is a different
// image from the same layers without it - so it belongs in the config, and the
// config is in the key (E486).
//
// `tests/parser-smoke.earth` writes all three forms.
func TestHealthcheckIsRecordedOnTheImage(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		line string
		want []string
		hc   func(*testing.T, *interp.Healthcheck)
	}{
		// `NONE` is a statement, not an absence: it *overrides* a healthcheck
		// the base image declared, and an image that dropped it would keep the
		// base's instead.
		"none": {line: "HEALTHCHECK NONE", want: []string{"NONE"}},

		"a command": {
			line: "HEALTHCHECK CMD true",
			want: []string{"CMD-SHELL", "true"},
		},

		"a command with timings": {
			line: "HEALTHCHECK --interval 15s --retries 2 --timeout 45s" +
				" --start-period 10s --start-interval 3s CMD echo one two three",
			want: []string{"CMD-SHELL", "echo one two three"},
			hc: func(t *testing.T, got *interp.Healthcheck) {
				t.Helper()

				for what, pair := range map[string][2]any{
					"interval":       {got.Interval, 15 * time.Second},
					"timeout":        {got.Timeout, 45 * time.Second},
					"start period":   {got.StartPeriod, 10 * time.Second},
					"start interval": {got.StartInterval, 3 * time.Second},
					"retries":        {got.Retries, 2},
				} {
					if pair[0] != pair[1] {
						t.Errorf("%s is %v, want %v", what, pair[0], pair[1])
					}
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n    "+tc.line+
				"\n    SAVE IMAGE thing:latest\n", testMain)
			if err != nil {
				t.Fatalf("planning %q: %v", tc.line, err)
			}

			if len(p.Images) != 1 {
				t.Fatalf("the plan declares %d images", len(p.Images))
			}

			got := p.Images[0].Config.Healthcheck
			if got == nil {
				t.Fatal("the image declares no healthcheck")
			}

			if len(got.Test) != len(tc.want) {
				t.Fatalf("the test is %q, want %q", got.Test, tc.want)
			}

			for i := range tc.want {
				if got.Test[i] != tc.want[i] {
					t.Errorf("the test is %q, want %q", got.Test, tc.want)
				}
			}

			if tc.hc != nil {
				tc.hc(t, got)
			}
		})
	}
}

// An image with a healthcheck is a different image.
//
// The config is in an image's identity, so a build that changed only this
// produces a different digest - which is what makes recording it worth anything
// rather than a comment on the side.
func TestAHealthcheckChangesTheImage(t *testing.T) {
	t.Parallel()

	const recipe = "\nmain:\n    FROM alpine:3.22\n%s    SAVE IMAGE thing:latest\n"

	with := imageID(t, versioned+fmtRecipe(recipe, "    HEALTHCHECK CMD true\n"))
	without := imageID(t, versioned+fmtRecipe(recipe, ""))

	if with == without {
		t.Error("an image with a healthcheck keys the same as one without," +
			" so the declaration reaches nothing that matters")
	}
}

// fmtRecipe splices a line into a recipe.
func fmtRecipe(recipe, line string) string {
	return strings.Replace(recipe, "%s", line, 1)
}

// imageID fingerprints what the declared image *is*.
//
// `HashImage` rather than the node the image is built from: the layers are the
// same either way - a healthcheck adds no step and no file - and what differs is
// the configuration those layers are wrapped in. Fingerprinting the node asked
// the wrong question and got the answer the question deserved (E486).
func imageID(t *testing.T, src string) string {
	t.Helper()

	p, err := interp.Build(src, testMain)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	if len(p.Images) != 1 {
		t.Fatalf("the plan declares %d images", len(p.Images))
	}

	h := ir.NewHasher()
	ir.HashImage(h, p.Images[0].Config.ToIR())

	return h.Sum().String()
}
