package features

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMustParseVersion(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		version  string
		expected []int
	}{
		{
			version:  "0.5",
			expected: []int{0, 5},
		},
		{
			version:  "0.67",
			expected: []int{0, 67},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()

			major, minor := mustParseVersion(tc.version)
			Equal(t, tc.expected[0], major)
			Equal(t, tc.expected[1], minor)
		})
	}
}

func TestFeaturesStringEnabled(t *testing.T) {
	t.Parallel()

	fts := &Features{
		Major:              0,
		Minor:              5,
		ReferencedSaveOnly: true,
	}
	s := fts.String()
	require.Equal(t, "VERSION --referenced-save-only 0.5", s)
}

func TestFeaturesStringDisabled(t *testing.T) {
	t.Parallel()

	fts := &Features{
		Major:              1,
		Minor:              1,
		ReferencedSaveOnly: false,
	}
	s := fts.String()
	require.Equal(t, "VERSION 1.1", s)
}

func TestApplyFlagOverrides(t *testing.T) {
	t.Parallel()

	fts := new(Features)
	err := ApplyFlagOverrides(fts, "referenced-save-only")
	require.NoError(t, err)
	require.True(t, fts.ReferencedSaveOnly)
	require.False(t, fts.UseCopyIncludePatterns)
	require.False(t, fts.ForIn)
	require.False(t, fts.RequireForceForUnsafeSaves)
	require.False(t, fts.NoImplicitIgnore)
}

func TestApplyFlagOverridesWithDashDashPrefix(t *testing.T) {
	t.Parallel()

	fts := new(Features)
	err := ApplyFlagOverrides(fts, "--referenced-save-only")
	require.NoError(t, err)
	require.True(t, fts.ReferencedSaveOnly)
	require.False(t, fts.UseCopyIncludePatterns)
	require.False(t, fts.ForIn)
	require.False(t, fts.RequireForceForUnsafeSaves)
	require.False(t, fts.NoImplicitIgnore)
}

func TestApplyFlagOverridesMultipleFlags(t *testing.T) {
	t.Parallel()

	fts := new(Features)
	err := ApplyFlagOverrides(fts, "referenced-save-only,use-copy-include-patterns,no-implicit-ignore")
	require.NoError(t, err)
	require.True(t, fts.ReferencedSaveOnly)
	require.True(t, fts.UseCopyIncludePatterns)
	require.False(t, fts.ForIn)
	require.False(t, fts.RequireForceForUnsafeSaves)
	require.True(t, fts.NoImplicitIgnore)
}

func TestApplyFlagOverridesEmptyString(t *testing.T) {
	t.Parallel()

	fts := new(Features)
	err := ApplyFlagOverrides(fts, "")
	require.NoError(t, err)
	require.False(t, fts.ReferencedSaveOnly)
	require.False(t, fts.UseCopyIncludePatterns)
	require.False(t, fts.ForIn)
	require.False(t, fts.RequireForceForUnsafeSaves)
	require.False(t, fts.NoImplicitIgnore)
}

func TestAvailableFlags(t *testing.T) {
	t.Parallel()

	// This test feels like it may be overkill, but it's nice to know that if we
	// introduce a typo we have to introduce it twice for our tests to still
	// pass.
	for _, tt := range []struct {
		flag  string
		field string
	}{
		// 0.5
		{"exec-after-parallel", "ExecAfterParallel"},
		{"parallel-load", "ParallelLoad"},
		{"use-registry-for-with-docker", "UseRegistryForWithDocker"},

		// 0.6
		{"for-in", "ForIn"},
		{"no-implicit-ignore", "NoImplicitIgnore"},
		{"referenced-save-only", "ReferencedSaveOnly"},
		{"require-force-for-unsafe-saves", "RequireForceForUnsafeSaves"},
		{"use-copy-include-patterns", "UseCopyIncludePatterns"},

		// 0.7
		{"check-duplicate-images", "CheckDuplicateImages"},
		{"ci-arg", "EarthlyCIArg"},
		{"earthly-git-author-args", "EarthlyGitAuthorArgs"},
		{"earthly-locally-arg", "EarthlyLocallyArg"},
		{"earthly-version-arg", "EarthlyVersionArg"},
		{"explicit-global", "ExplicitGlobal"},
		{"git-commit-author-timestamp", "GitCommitAuthorTimestamp"},
		{"new-platform", "NewPlatform"},
		{"no-tar-build-output", "NoTarBuildOutput"},
		{"save-artifact-keep-own", "SaveArtifactKeepOwn"},
		{"shell-out-anywhere", "ShellOutAnywhere"},
		{"use-cache-command", "UseCacheCommand"},
		{"use-chmod", "UseChmod"},
		{"use-copy-link", "UseCopyLink"},
		{"use-host-command", "UseHostCommand"},
		{"use-no-manifest-list", "UseNoManifestList"},
		{"use-project-secrets", "UseProjectSecrets"},
		{"wait-block", "WaitBlock"},

		// unreleased
		{"no-use-registry-for-with-docker", "NoUseRegistryForWithDocker"},
		{"try", "TryFinally"},
		{"no-network", "NoNetwork"},
		{"arg-scope-and-set", "ArgScopeSet"},
		{"earthly-ci-runner-arg", "EarthlyCIRunnerArg"},
		{"use-docker-ignore", "UseDockerIgnore"},
	} {
		t.Run(tt.flag, func(t *testing.T) {
			t.Parallel()

			var fts Features

			err := ApplyFlagOverrides(&fts, tt.flag)
			require.NoError(t, err)

			field := reflect.ValueOf(fts).FieldByName(tt.field)
			require.True(t, field.IsValid(), "field %v does not exist on %T", tt.field, fts)
			val, ok := field.Interface().(bool)
			require.True(t, ok, "field %v was not a boolean", tt.field)
			require.True(t, val, "expected field %v to be set to true by flag %v", tt.field, tt.flag)
		})
	}
}

func TestContext(t *testing.T) {
	t.Parallel()

	fts := new(Features)

	t.Run("features can be set and retrieved from context", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		newCtx, err := fts.WithContext(ctx)
		require.Equal(t, fts, FromContext(newCtx))
		require.NoError(t, err)
	})

	t.Run("context cannot be set more than once", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		ctx2, err := fts.WithContext(ctx)
		require.NoError(t, err)
		ctx3, err := fts.WithContext(ctx2)
		require.Error(t, err)
		require.Equal(t, ctx2, ctx3)
	})

	t.Run("returns nil when not set in context", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		require.Nil(t, FromContext(ctx))
	})
}

func TestProcessFlags(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		f                *Features
		expectedFeatures *Features
		name             string
		expectedWarnings []string
	}{
		{
			name: "version v0.4",
			f: &Features{
				Major: 0,
				Minor: 4,
			},
			expectedWarnings: []string{},
			expectedFeatures: &Features{
				Major: 0,
				Minor: 4,
			},
		},
		{
			name: "version v0.5: now warning message",
			f: &Features{
				Major: 0,
				Minor: 5,
			},
			expectedWarnings: []string{},
			expectedFeatures: &Features{
				ExecAfterParallel:        true,
				ParallelLoad:             true,
				UseRegistryForWithDocker: true,
				Major:                    0,
				Minor:                    5,
			},
		},
		{
			name: "version v0.6: exist warning message",
			f: &Features{
				ExecAfterParallel: true,
				ParallelLoad:      true,
				Major:             0,
				Minor:             5,
			},
			expectedWarnings: []string{
				"--exec-after-parallel",
				"--parallel-load",
			},
			expectedFeatures: &Features{
				ExecAfterParallel:        true,
				ParallelLoad:             true,
				UseRegistryForWithDocker: true,
				Major:                    0,
				Minor:                    5,
			},
		},
		{
			name: "version v0.6: exist warning message",
			f: &Features{
				ExecAfterParallel:          true,
				ParallelLoad:               true,
				UseRegistryForWithDocker:   true,
				ForIn:                      true,
				NoImplicitIgnore:           true,
				ReferencedSaveOnly:         true,
				RequireForceForUnsafeSaves: true,
				UseCopyIncludePatterns:     true,
				Major:                      0,
				Minor:                      6,
			},
			expectedWarnings: []string{
				"--exec-after-parallel",
				"--parallel-load",
				"--use-registry-for-with-docker",
				"--for-in",
				"--no-implicit-ignore",
				"--referenced-save-only",
				"--require-force-for-unsafe-saves",
				"--use-copy-include-patterns",
			},
			expectedFeatures: &Features{
				ExecAfterParallel:          true,
				ParallelLoad:               true,
				UseRegistryForWithDocker:   true,
				ForIn:                      true,
				NoImplicitIgnore:           true,
				ReferencedSaveOnly:         true,
				RequireForceForUnsafeSaves: true,
				UseCopyIncludePatterns:     true,
				Major:                      0,
				Minor:                      6,
			},
		},
		{
			name: "version v0.7: exist warning message",
			f: &Features{
				CheckDuplicateImages:     true,
				EarthlyCIArg:             true,
				EarthlyGitAuthorArgs:     true,
				EarthlyLocallyArg:        true,
				EarthlyVersionArg:        true,
				ExplicitGlobal:           true,
				GitCommitAuthorTimestamp: true,
				NewPlatform:              true,
				NoTarBuildOutput:         true,
				SaveArtifactKeepOwn:      true,
				ShellOutAnywhere:         true,
				UseCacheCommand:          true,
				UseChmod:                 true,
				UseCopyLink:              true,
				UseHostCommand:           true,
				UseNoManifestList:        true,
				UseProjectSecrets:        true,
				WaitBlock:                true,
				Major:                    0,
				Minor:                    7,
			},
			expectedWarnings: []string{
				"--check-duplicate-images",
				"--ci-arg",
				"--earthly-git-author-args",
				"--earthly-locally-arg",
				"--earthly-version-arg",
				"--explicit-global",
				"--git-commit-author-timestamp",
				"--new-platform",
				"--no-tar-build-output",
				"--save-artifact-keep-own",
				"--shell-out-anywhere",
				"--use-cache-command",
				"--use-chmod",
				"--use-copy-link",
				"--use-host-command",
				"--use-no-manifest-list",
				"--use-project-secrets",
				"--wait-block",
			},
			expectedFeatures: &Features{
				ExecAfterParallel:          true,
				ParallelLoad:               true,
				UseRegistryForWithDocker:   true,
				ForIn:                      true,
				NoImplicitIgnore:           true,
				ReferencedSaveOnly:         true,
				RequireForceForUnsafeSaves: true,
				UseCopyIncludePatterns:     true,
				CheckDuplicateImages:       true,
				EarthlyCIArg:               true,
				EarthlyGitAuthorArgs:       true,
				EarthlyLocallyArg:          true,
				EarthlyVersionArg:          true,
				ExplicitGlobal:             true,
				GitCommitAuthorTimestamp:   true,
				NewPlatform:                true,
				NoTarBuildOutput:           true,
				SaveArtifactKeepOwn:        true,
				ShellOutAnywhere:           true,
				UseCacheCommand:            true,
				UseChmod:                   true,
				UseCopyLink:                true,
				UseHostCommand:             true,
				UseNoManifestList:          true,
				UseProjectSecrets:          true,
				WaitBlock:                  true,
				Major:                      0,
				Minor:                      7,
			},
		},
		{
			name: "version v0.8: no warning message",
			f: &Features{
				Major: 0,
				Minor: 8,
			},
			expectedWarnings: []string{},
			expectedFeatures: &Features{
				ExecAfterParallel:          true,
				ParallelLoad:               true,
				UseRegistryForWithDocker:   true,
				ForIn:                      true,
				NoImplicitIgnore:           true,
				ReferencedSaveOnly:         true,
				RequireForceForUnsafeSaves: true,
				UseCopyIncludePatterns:     true,
				CheckDuplicateImages:       true,
				EarthlyCIArg:               true,
				EarthlyGitAuthorArgs:       true,
				EarthlyLocallyArg:          true,
				EarthlyVersionArg:          true,
				ExplicitGlobal:             true,
				GitCommitAuthorTimestamp:   true,
				NewPlatform:                true,
				NoTarBuildOutput:           true,
				SaveArtifactKeepOwn:        true,
				ShellOutAnywhere:           true,
				UseCacheCommand:            true,
				UseChmod:                   true,
				UseCopyLink:                true,
				UseHostCommand:             true,
				UseNoManifestList:          true,
				UseProjectSecrets:          true,
				WaitBlock:                  true,

				NoNetwork:                       true,
				ArgScopeSet:                     true,
				UseDockerIgnore:                 true,
				PassArgs:                        true,
				GlobalCache:                     true,
				CachePersistOption:              true,
				GitRefs:                         true,
				UseVisitedUpfrontHashCollection: true,
				UseFunctionKeyword:              true,
				Major:                           0,
				Minor:                           8,
			},
		},
	}

	for _, tc := range testCases {
		warnings, err := tc.f.ProcessFlags()
		require.NoError(t, err, tc.name)

		require.Len(t, warnings, len(tc.expectedWarnings), tc.name)
		require.Equal(t, tc.expectedWarnings, warnings, tc.name)

		require.Equal(t, tc.expectedFeatures, tc.f, tc.name)
	}
}
