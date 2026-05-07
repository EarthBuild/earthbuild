package subcmd

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/ast"
	"github.com/EarthBuild/earthbuild/ast/spec"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

var captureStdoutMu sync.Mutex

func TestDocTargetFixtures(t *testing.T) {
	t.Parallel()

	ef, ftrs := parseDocFixture(t, "target-docs.earth")
	cliCtx := cli.NewContext(cli.NewApp(), nil, nil)
	doc := &Doc{}

	t.Run("documented target", func(t *testing.T) {
		t.Parallel()

		tgt := mustFindDocTarget(t, ef, "documented-target")
		out := captureStdout(t, func() error {
			return doc.documentSingleTarget(cliCtx, "", ftrs, ef.BaseRecipe, tgt, false)
		})

		require.Contains(t, out, "+documented-target\n")
		require.Contains(t, out, "documented-target is a target with documentation\n")
		require.Contains(t, out, "that spans multiple lines.\n")
		require.Contains(t, out, "It also has a separator between paragraphs.\n")
	})

	t.Run("undocumented target fails", func(t *testing.T) {
		t.Parallel()

		tgt := mustFindDocTarget(t, ef, "undocumented-target")
		err := doc.documentSingleTarget(cliCtx, "", ftrs, ef.BaseRecipe, tgt, false)
		require.Error(t, err)
		require.ErrorContains(t, err, "no doc comment found")
	})

	t.Run("incorrectly documented target fails", func(t *testing.T) {
		t.Parallel()

		tgt := mustFindDocTarget(t, ef, "incorrectly-documented-target")
		err := doc.documentSingleTarget(cliCtx, "", ftrs, ef.BaseRecipe, tgt, false)
		require.Error(t, err)
		require.ErrorContains(t, err, "no doc comment found")
	})
}

func TestDocRecipeBlockFixture(t *testing.T) {
	t.Parallel()

	ef, ftrs := parseDocFixture(t, "doc-recipe-block.earth")
	cliCtx := cli.NewContext(cli.NewApp(), nil, nil)
	tgt := mustFindDocTarget(t, ef, "foo")

	blockIO, err := parseDocSections(cliCtx, ftrs, ef.BaseRecipe, tgt.Recipe)
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

	out := captureStdout(t, func() error {
		return (&Doc{}).documentSingleTarget(cliCtx, "", ftrs, ef.BaseRecipe, tgt, true)
	})

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

func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()

	captureStdoutMu.Lock()
	defer captureStdoutMu.Unlock()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = w

	defer func() {
		os.Stdout = oldStdout
	}()

	fnErr := fn()

	require.NoError(t, w.Close())

	require.NoError(t, fnErr)

	out, err := io.ReadAll(r)
	require.NoError(t, err)

	require.NoError(t, r.Close())

	return string(out)
}
