package buildcontext

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/moby/patternmatcher"
)

//nolint:goconst
func Test_readExcludes(t *testing.T) {
	t.Parallel()

	// implicitAndSecret is the default set of excludes applied when no feature
	// disables them.
	implicitAndSecret := slices.Concat(ImplicitExcludes, SecretExcludes)

	testcases := []struct {
		wantErr               error
		name                  string
		earthIgnoreContents   string
		earthlyIgnoreContents string
		dockerIgnoreContents  string
		wantExcludes          []string
		opts                  excludeOpts
	}{
		{
			name:                  "only .earthlyignore",
			earthlyIgnoreContents: `foobar/`,
			wantExcludes: []string{
				"foobar", ".tmp-earth-out/", "build.earth", "Earthfile", ".earthignore", ".earthlyignore", "**/.secret",
			},
		},
		{
			name:                "only .earthignore",
			earthIgnoreContents: `foobar/`,
			wantExcludes: []string{
				"foobar", ".tmp-earth-out/", "build.earth", "Earthfile", ".earthignore", ".earthlyignore", "**/.secret",
			},
		},
		{
			name:                 "only .dockerignore",
			dockerIgnoreContents: `foobar/`,
			opts:                 excludeOpts{useDockerIgnore: true},
			wantExcludes: []string{
				"foobar", ".tmp-earth-out/", "build.earth", "Earthfile", ".earthignore", ".earthlyignore", "**/.secret",
			},
		},
		{
			name:                  "only .earthlyignore with no implicit ignore",
			earthlyIgnoreContents: `foobar/`,
			opts:                  excludeOpts{noImplicitIgnore: true},
			wantExcludes:          []string{"foobar", "**/.secret"},
		},
		{
			name:                "only .earthignore with no implicit ignore",
			earthIgnoreContents: `foobar/`,
			opts:                excludeOpts{noImplicitIgnore: true},
			wantExcludes:        []string{"foobar", "**/.secret"},
		},
		{
			name:                 "only .dockerignore with no implicit ignore",
			dockerIgnoreContents: `foobar/`,
			opts:                 excludeOpts{noImplicitIgnore: true, useDockerIgnore: true},
			wantExcludes:         []string{"foobar", "**/.secret"},
		},
		{
			name:                 ".dockerignore re-including .secret is overridden",
			dockerIgnoreContents: "*\n!.secret\n",
			opts:                 excludeOpts{noImplicitIgnore: true, useDockerIgnore: true},
			wantExcludes:         []string{"*", "!.secret", "**/.secret"},
		},
		{
			name:         "no ignore file, default to implicit rules",
			wantExcludes: implicitAndSecret,
		},
		{
			name:         "no ignore file and no implicit ignore still excludes secrets",
			opts:         excludeOpts{noImplicitIgnore: true},
			wantExcludes: SecretExcludes,
		},
		{
			name:         "no ignore file and no implicit secret ignore",
			opts:         excludeOpts{noImplicitSecretIgnore: true},
			wantExcludes: ImplicitExcludes,
		},
		{
			name:         "no ignore file and all implicit ignores disabled",
			opts:         excludeOpts{noImplicitIgnore: true, noImplicitSecretIgnore: true},
			wantExcludes: []string{},
		},
		{
			name:                  "both .earthignore and .earthlyignore results in error",
			earthlyIgnoreContents: `foobar/`,
			earthIgnoreContents:   `foobar/`,
			wantExcludes:          implicitAndSecret,
			wantErr:               errDuplicateIgnoreFile,
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			for name, contents := range map[string]string{
				earthIgnoreFile:   testcase.earthIgnoreContents,
				earthlyIgnoreFile: testcase.earthlyIgnoreContents,
				dockerIgnoreFile:  testcase.dockerIgnoreContents,
			} {
				if contents == "" {
					continue
				}

				err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600)
				if err != nil {
					t.Fatalf("failed to write %s file: %v", name, err)
				}
			}

			excludes, err := readExcludes(dir, testcase.opts)
			if !errors.Is(err, testcase.wantErr) {
				t.Errorf("readExcludes() error = %v, want %v", err, testcase.wantErr)
			}

			if !slices.Equal(excludes, testcase.wantExcludes) {
				t.Errorf("readExcludes() = %v, want %v", excludes, testcase.wantExcludes)
			}
		})
	}
}

// TestSecretExcludesMatch checks that SecretExcludes match the secret file at any
// depth of a build context, even when an ignore file tries to re-include it,
// while leaving similarly named files alone.
func TestSecretExcludesMatch(t *testing.T) {
	t.Parallel()

	pm, err := patternmatcher.New(slices.Concat([]string{"*", "!.secret", "!sub"}, SecretExcludes))
	if err != nil {
		t.Fatalf("patternmatcher.New: %v", err)
	}

	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: ".secret", want: true},
		{path: "sub/.secret", want: true},
		{path: "a/b/c/.secret", want: true},
		{path: ".secret/key", want: true},
		{path: "sub/.secrets", want: false},
		{path: "sub/my.secret", want: false},
		{path: "sub", want: false},
	} {
		got, err := pm.MatchesOrParentMatches(tc.path)
		if err != nil {
			t.Fatalf("MatchesOrParentMatches(%q): %v", tc.path, err)
		}

		if got != tc.want {
			t.Errorf("MatchesOrParentMatches(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
