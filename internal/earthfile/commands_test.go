package earthfile

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// declaredCmdConstants reads the Cmd constants straight out of the lexer
// source so the exported list cannot silently drift from the canonical set.
func declaredCmdConstants(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()

	file, err := goparser.ParseFile(fset, "lex.go", nil, 0)
	require.NoError(t, err)

	var declared []string

	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}

		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "Cmd" {
			return true
		}

		for _, value := range spec.Values {
			literal, ok := value.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}

			unquoted, unquoteErr := strconv.Unquote(literal.Value)
			require.NoError(t, unquoteErr)

			declared = append(declared, unquoted)
		}

		return true
	})

	return declared
}

func TestCommandsMatchesCanonicalConstants(t *testing.T) {
	t.Parallel()

	declared := declaredCmdConstants(t)
	require.NotEmpty(t, declared, "no Cmd constants found in lex.go")

	actual := make([]string, 0, len(Commands()))
	for _, cmd := range Commands() {
		actual = append(actual, string(cmd))
	}

	slices.Sort(declared)
	slices.Sort(actual)

	require.Equal(t, declared, actual,
		"Commands() must list every Cmd constant; add new commands to commands.go")
}

func TestCommandsIsSortedAndUnique(t *testing.T) {
	t.Parallel()

	commands := Commands()
	require.True(t, slices.IsSorted(commands), "Commands() must be sorted")

	unique := slices.Compact(slices.Clone(commands))
	require.Equal(t, len(commands), len(unique), "Commands() must not contain duplicates")
}

func TestCommandsReturnsACopy(t *testing.T) {
	t.Parallel()

	first := Commands()
	require.NotEmpty(t, first)
	first[0] = "MUTATED"

	require.NotEqual(t, Cmd("MUTATED"), Commands()[0], "Commands() must not expose shared state")
}

func TestMultiWordCommands(t *testing.T) {
	t.Parallel()

	multi := MultiWordCommands()
	require.Contains(t, multi, CmdSaveArtifact)
	require.Contains(t, multi, CmdFromDockerfile)
	require.NotContains(t, multi, CmdRun)

	for _, cmd := range multi {
		require.Contains(t, Commands(), cmd, "multi-word command must also be listed in Commands()")
	}
}
