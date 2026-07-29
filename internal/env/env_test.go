package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWarningsFor(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		environ []string
		want    []string
	}{
		{
			name:    "no earthly vars",
			environ: []string{"HOME=/root", "EARTH_CONFIG=/tmp/config.yml", "PATH=/usr/bin"},
			want:    nil,
		},
		{
			name:    "single earthly var",
			environ: []string{"EARTHLY_INSTALLATION_NAME=earthly-test2"},
			want:    []string{"WARNING: EARTHLY_INSTALLATION_NAME is deprecated. Use EARTH_INSTALLATION_NAME."},
		},
		{
			name:    "multiple earthly vars sorted",
			environ: []string{"EARTHLY_PUSH=true", "HOME=/root", "EARTHLY_CONFIG=/tmp/config.yml"},
			want: []string{
				"WARNING: EARTHLY_CONFIG is deprecated. Use EARTH_CONFIG.",
				"WARNING: EARTHLY_PUSH is deprecated. Use EARTH_PUSH.",
			},
		},
		{
			name:    "var with empty value still warns",
			environ: []string{"EARTHLY_VERBOSE="},
			want:    []string{"WARNING: EARTHLY_VERBOSE is deprecated. Use EARTH_VERBOSE."},
		},
		{
			name:    "earth prefix is not flagged",
			environ: []string{"EARTH_GIT_HASH=abc123"},
			want:    nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, warningsFor(tc.environ))
		})
	}
}

func TestLookup(t *testing.T) {
	strp := func(s string) *string { return &s }

	testCases := []struct {
		name      string
		earth     *string // value for EARTH_<suffix>, nil means unset
		earthly   *string // value for EARTHLY_<suffix>, nil means unset
		wantValue string
		wantOK    bool
	}{
		{
			name:      "prefers EARTH_ over deprecated EARTHLY_",
			earth:     strp("new"),
			earthly:   strp("old"),
			wantValue: "new",
			wantOK:    true,
		},
		{
			name:      "falls back to deprecated EARTHLY_",
			earthly:   strp("old"),
			wantValue: "old",
			wantOK:    true,
		},
		{
			name:      "missing returns false",
			wantValue: "",
			wantOK:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Do not use t.Parallel() because t.Setenv modifies process-wide state.
			if tc.earth != nil {
				t.Setenv(Prefix+"LOOKUP_TEST", *tc.earth)
			}

			if tc.earthly != nil {
				t.Setenv(DeprecatedPrefix+"LOOKUP_TEST", *tc.earthly)
			}

			v, ok := Lookup("LOOKUP_TEST")
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantValue, v)
		})
	}
}
