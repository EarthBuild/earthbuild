package subcmd

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/ast"
	"github.com/EarthBuild/earthbuild/ast/spec"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/stretchr/testify/require"
)

func TestParseDocTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		wantTarget  string
		wantErrLike string
		wantSingle  bool
	}{
		{name: "empty documents all base targets", path: "", wantTarget: docBaseTarget, wantSingle: false},
		{name: "local dir documents all base targets", path: ".", wantTarget: docBaseTarget, wantSingle: false},
		{name: "explicit target is single", path: "+build", wantTarget: "+build", wantSingle: true},
		{name: "pathed target is single", path: "./foo+build", wantTarget: "./foo+build", wantSingle: true},
		{name: "remote path rejected", path: "github.com/foo/bar+x", wantErrLike: "remote-paths are not currently supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target, single, err := parseDocTarget(tt.path)
			if tt.wantErrLike != "" {
				require.ErrorContains(t, err, tt.wantErrLike)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantTarget, target.String())
			require.Equal(t, tt.wantSingle, single)
		})
	}
}

func TestDocTargetFixtures(t *testing.T) {
	t.Parallel()

	ef, ftrs := parseDocFixture(t, "target-docs.earth")

	t.Run("documented target", func(t *testing.T) {
		t.Parallel()

		tgt := mustFindDocTarget(t, ef, "documented-target")
		out, err := captureDoc(func(d *Doc) error {
			return d.documentSingleTarget("", ftrs, ef.BaseRecipe, tgt, false)
		})
		require.NoError(t, err)

		require.Contains(t, out, "+documented-target\n")
		require.Contains(t, out, "documented-target is a target with documentation\n")
		require.Contains(t, out, "that spans multiple lines.\n")
		require.Contains(t, out, "It also has a separator between paragraphs.\n")
	})

	t.Run("undocumented target fails", func(t *testing.T) {
		t.Parallel()

		tgt := mustFindDocTarget(t, ef, "undocumented-target")
		err := (&Doc{}).documentSingleTarget("", ftrs, ef.BaseRecipe, tgt, false)
		require.Error(t, err)
		require.ErrorContains(t, err, "no doc comment found")
		require.ErrorIs(t, err, errNoDocComment)
	})

	t.Run("incorrectly documented target fails", func(t *testing.T) {
		t.Parallel()

		tgt := mustFindDocTarget(t, ef, "incorrectly-documented-target")
		err := (&Doc{}).documentSingleTarget("", ftrs, ef.BaseRecipe, tgt, false)
		require.Error(t, err)
		require.ErrorContains(t, err, "no doc comment found")
		require.ErrorIs(t, err, errNoDocComment)
	})
}

func TestDocRecipeBlockFixture(t *testing.T) {
	t.Parallel()

	ef, ftrs := parseDocFixture(t, "doc-recipe-block.earth")
	tgt := mustFindDocTarget(t, ef, "foo")

	blockIO, err := parseDocSections(ftrs, ef.BaseRecipe, tgt.Recipe)
	require.NoError(t, err)
	require.Equal(t, []string{"--requiredArg"}, docIdentifiers(blockIO.requiredArgs))
	require.Equal(
		t,
		[]string{"--globalArg", "--withDefault=foo", "--withDocs", "--withoutDocs"},
		docIdentifiers(blockIO.optionalArgs),
	)
	require.Equal(t, []string{"bar.txt", "baz.txt"}, docIdentifiers(blockIO.artifacts))
	require.Equal(
		t,
		[]string{"baz.txt -> out/baz.txt", "bacon.txt -> out/eggs.txt"},
		docIdentifiers(blockIO.localArtifacts),
	)
	require.Equal(t, []string{"baz", "bar", "bacon, eggs"}, docIdentifiers(blockIO.images))
	require.NotEmpty(t, blockIO.optionalArgs[0].body)
	require.Empty(t, blockIO.optionalArgs[3].body)
	require.NotEmpty(t, blockIO.artifacts[0].body)
	require.Empty(t, blockIO.artifacts[1].body)
	require.NotEmpty(t, blockIO.localArtifacts[0].body)
	require.NotEmpty(t, blockIO.images[0].body)
	require.Empty(t, blockIO.images[1].body)

	out, err := captureDoc(func(d *Doc) error {
		return d.documentSingleTarget("", ftrs, ef.BaseRecipe, tgt, true)
	})
	require.NoError(t, err)

	require.Contains(t, out, "+foo --requiredArg")
	require.Contains(t, out, "[--globalArg] [--withDefault=foo] [--withDocs] [--withoutDocs]\n")
	require.Contains(t, out, "REQUIRED ARGS:")
	require.Contains(t, out, "OPTIONAL ARGS:")
	require.Contains(t, out, "ARTIFACTS:")
	require.Contains(t, out, "LOCAL ARTIFACTS:")
	require.Contains(t, out, "IMAGES:")
}

func parseDocFixture(t *testing.T, fixture string) (spec.Earthfile, *features.Features) {
	t.Helper()

	ef, err := ast.ParseOpts(ast.FromPath(filepath.Join("testdata", fixture)))
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	return ef, ftrs
}

func mustFindDocTarget(t *testing.T, ef spec.Earthfile, name string) spec.Target {
	t.Helper()

	tgt, err := findTarget(ef, name)
	require.NoError(t, err)

	return tgt
}

func docIdentifiers(sections []docSection) []string {
	ids := make([]string, len(sections))
	for i, section := range sections {
		ids[i] = section.identifier
	}

	return ids
}

// captureDoc runs fn against a Doc that writes into an in-memory buffer,
// returning the rendered output. No global stdout hijacking, so it is safe
// under t.Parallel() and cannot deadlock on a full pipe.
func captureDoc(fn func(*Doc) error) (string, error) {
	var buf bytes.Buffer

	err := fn(&Doc{out: &buf})

	return buf.String(), err
}
