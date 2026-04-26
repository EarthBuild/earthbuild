package ast_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/ast"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	namedReader := namedStringReader{strings.NewReader("VERSION 0.6")}
	ver, err := ast.ParseVersionOpts(ast.FromReader(&namedReader))
	r := require.New(t)
	r.NoError(err)
	r.Len(ver.Args, 1)
	r.Equal("0.6", ver.Args[0])
	r.Nil(ver.SourceLocation)
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

			ef, err := ast.ParseOpts(ast.FromPath(filepath.Join("..", "tests", "version", fixture)))
			require.NoError(t, err)
			require.NotNil(t, ef.Version)

			ftrs, _, err := features.Get(ef.Version)
			require.NoError(t, err)
			_, err = ftrs.ProcessFlags()
			require.NoError(t, err)
		})
	}
}

func TestInvalidVersionFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fixture      string
		wantErr      string
		parseVersion bool
	}{
		{
			fixture: "invalid-major-version.earth",
			wantErr: "Earthfile version is invalid, supported versions are 0.6, 0.7, or 0.8",
		},
		{
			fixture: "invalid-minor-version.earth",
			wantErr: "Earthfile version is invalid, supported versions are 0.6, 0.7, or 0.8",
		},
		{
			fixture:      "invalid-patch-version.earth",
			wantErr:      "unexpected VERSION arguments; should be VERSION [flags] <major-version>.<minor-version>",
			parseVersion: true,
		},
		{
			fixture:      "invalid-format-version.earth",
			wantErr:      "unexpected VERSION arguments; should be VERSION [flags] <major-version>.<minor-version>",
			parseVersion: true,
		},
		{
			fixture:      "invalid-feature-flag-override.earth",
			wantErr:      "bool flag `--referenced-save-only' cannot have an argument",
			parseVersion: true,
		},
	}

	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("..", "tests", "version", test.fixture)
			if test.parseVersion {
				version, err := ast.ParseVersion(path, false)
				require.NoError(t, err)

				_, _, err = features.Get(version)
				require.Error(t, err)
				require.ErrorContains(t, err, test.wantErr)

				return
			}

			ef, err := ast.ParseOpts(ast.FromPath(path))
			if err == nil {
				ftrs, _, ftrsErr := features.Get(ef.Version)
				if ftrsErr == nil {
					_, ftrsErr = ftrs.ProcessFlags()
				}
				err = ftrsErr
			}
			require.Error(t, err)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}
