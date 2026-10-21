package env

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unsetEnv snapshots each key via t.Setenv (so cleanup restores the caller's
// environment) and then unsets it for the duration of the test.
func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()

	for _, k := range keys {
		t.Setenv(k, "")
		require.NoError(t, os.Unsetenv(k))
	}
}

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
		{
			// The scan cannot tell a user-set variable from one earthbuild set
			// on itself, so the published images must only ever set EARTH_
			// names.
			name: "environment of the published image is silent",
			environ: []string{
				"EARTH_CACHE_VERSION=2",
				"EARTH_DISABLE_REMOTE_REGISTRY_PROXY=true",
				"EARTH_GIT_HASH=abc123",
				"EARTH_RESET_TMP_DIR=false",
				"EARTH_TMP_DIR=/tmp/earthbuild",
				"PATH=/usr/bin",
			},
			want: nil,
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
			unsetEnv(t, Prefix+"LOOKUP_TEST", DeprecatedPrefix+"LOOKUP_TEST")

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

func TestBool(t *testing.T) {
	const (
		key   = Prefix + "TEST_BOOL"
		truth = "true"
	)

	testCases := []struct {
		environ map[string]string
		name    string
		wantErr string
		want    bool
	}{
		{name: "unset", want: false},
		{name: "empty", environ: map[string]string{key: ""}, want: false},
		{name: "whitespace-only", environ: map[string]string{key: "  \t "}, want: false},
		{name: "whitespace-padded", environ: map[string]string{key: "  true\n"}, want: true},
		{name: "word", environ: map[string]string{key: "TRUE"}, want: true},
		{name: "letter", environ: map[string]string{key: "t"}, want: true},
		{name: "digit", environ: map[string]string{key: "1"}, want: true},
		{name: "negative-word", environ: map[string]string{key: "False"}, want: false},
		{name: "negative-digit", environ: map[string]string{key: "0"}, want: false},
		{
			name:    "garbage",
			environ: map[string]string{key: "yes"},
			want:    false,
			wantErr: `EARTH_TEST_BOOL is set to "yes"`,
		},
		{
			name:    "deprecated prefix",
			environ: map[string]string{DeprecatedPrefix + "TEST_BOOL": truth},
			want:    true,
		},
		{
			// The error must name the variable the user actually set, not the one
			// they would have to go looking for.
			name:    "deprecated prefix garbage",
			environ: map[string]string{DeprecatedPrefix + "TEST_BOOL": "yes"},
			want:    false,
			wantErr: `EARTHLY_TEST_BOOL is set to "yes"`,
		},
		{
			// Same rule as Lookup: EARTH_ wins, so a stale EARTHLY_ cannot override it.
			name:    "both prefixes",
			environ: map[string]string{key: "false", DeprecatedPrefix + "TEST_BOOL": truth},
			want:    false,
		},
		{
			name:    "empty primary overrides deprecated",
			environ: map[string]string{key: "", DeprecatedPrefix + "TEST_BOOL": truth},
			want:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			unsetEnv(t, key, DeprecatedPrefix+"TEST_BOOL")

			for k, value := range tc.environ {
				t.Setenv(k, value)
			}

			got, err := Bool("TEST_BOOL")
			assert.Equal(t, tc.want, got)

			if tc.wantErr == "" {
				require.NoError(t, err)

				return
			}

			// The message must name the variable and the offending value, so the
			// user can fix it without grepping the source.
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}
