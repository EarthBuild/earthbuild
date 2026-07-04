package earthfile

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	ver, err := parseVersion("VERSION 0.6", "Earthfile")
	r := require.New(t)
	r.NoError(err)
	r.Len(ver.Args, 1)
	r.Equal("0.6", ver.Args[0])
	r.Nil(ver.SourceLocation)
}

func TestParseVersionFile_Error(t *testing.T) {
	t.Parallel()

	_, err := ParseVersionFile("non-existent-file")
	r := require.New(t)
	r.Error(err)
	r.ErrorContains(err, "earthfile: unable to open file")
}

func TestVersionFixtures(t *testing.T) {
	t.Parallel()

	validFixtures := []string{
		"single-line.earth",
		"single-line-with-args.earth",
		"single-line-with-comment.earth",
		"multi-line.earth",
		"multi-line-with-comment.earth",
		"multi-line-with-comment2.earth",
		"multi-line-with-comment3.earth",
		"multi-line-with-comment4.earth",
		"multi-line-with-args.earth",
		"multi-line-with-args2.earth",
		"multi-line-with-empty-newline.earth",
		"version-only-import.earth",
		"version-only.earth",
		"comment-and-whitespace-before-version.earth",
		"whitespace-then-version.earth",
	}

	for _, fixture := range validFixtures {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			tree, err := ParseFile(filepath.Join("testdata", "version", fixture))
			require.NoError(t, err)
			require.NotNil(t, tree.Version)
		})
	}
}

func TestInvalidVersionFixtures(t *testing.T) {
	t.Parallel()

	// The parser's version validator funnels every unsupported VERSION value
	// (bad major/minor/patch, or an unrecognised trailing token) to the same
	// message. Feature-flag validation (e.g. invalid-feature-flag-override.earth)
	// lives in the features package, not here, so it is exercised by the
	// integration tests under tests/version, not by this unit test.
	const wantErr = "invalid VERSION in Earthfile, supported versions are 0.6, 0.7, or 0.8"

	fixtures := []string{
		"invalid-major-version.earth",
		"invalid-minor-version.earth",
		"invalid-patch-version.earth",
		"invalid-format-version.earth",
	}

	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			_, err := ParseFile(filepath.Join("testdata", "version", fixture))
			require.Error(t, err)
			require.ErrorContains(t, err, wantErr)
		})
	}
}
