package analyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

func flagNames(flags []Flag) []string {
	names := make([]string, 0, len(flags))
	for _, flag := range flags {
		names = append(names, flag.Name)
	}

	return names
}

func findFlag(t *testing.T, flags []Flag, name string) Flag {
	t.Helper()

	index := slices.IndexFunc(flags, func(flag Flag) bool { return flag.Name == name })
	require.NotEqual(t, -1, index, "flag %q not found in %v", name, flagNames(flags))

	return flags[index]
}

func TestCommandFlagsReadsCanonicalOptions(t *testing.T) {
	t.Parallel()

	flags := CommandFlags("RUN")
	require.NotEmpty(t, flags)

	push := findFlag(t, flags, "push")
	require.False(t, push.Argument, "a bool option takes no value")
	require.Contains(t, push.Description, "push mode")

	secret := findFlag(t, flags, "secret")
	require.True(t, secret.Argument, "a string option takes a value")
	require.Equal(t, "Make available a secret", secret.Description)
}

func TestCommandFlagsAreSortedAndUnique(t *testing.T) {
	t.Parallel()

	names := flagNames(CommandFlags("RUN"))
	require.True(t, slices.IsSorted(names), "flags must be sorted")
	require.Equal(t, len(names), len(slices.Compact(slices.Clone(names))))
}

func TestCommandFlagsForMultiWordCommand(t *testing.T) {
	t.Parallel()

	require.Contains(t, flagNames(CommandFlags("SAVE ARTIFACT")), "if-exists")
	require.Contains(t, flagNames(CommandFlags("SAVE IMAGE")), "cache-from")
}

func TestCommandFlagsForBlockCommand(t *testing.T) {
	t.Parallel()

	// WITH DOCKER is a block prefix rather than a single canonical keyword, so
	// its options hang off DOCKER.
	require.Contains(t, flagNames(CommandFlags("DOCKER")), "load")
}

func TestCommandFlagsForCommandWithoutOptions(t *testing.T) {
	t.Parallel()

	require.Empty(t, CommandFlags("WORKDIR"))
	require.Empty(t, CommandFlags("PROJECT"), "an empty options struct contributes no flags")
	require.Empty(t, CommandFlags("NOPE"))
	require.Empty(t, CommandFlags(""))
}

// declaredOptionStructs reads the option struct names straight out of the
// cmdopts source so the command mapping cannot silently drift.
func declaredOptionStructs(t *testing.T) []string {
	t.Helper()

	path := filepath.Join("..", "..", "..", "earthfile2llb", "cmdopts", "opts.go")

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)

	var declared []string

	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok {
			return true
		}

		if _, isStruct := spec.Type.(*ast.StructType); isStruct && spec.Name.IsExported() {
			declared = append(declared, spec.Name.Name)
		}

		return true
	})

	return declared
}

func TestEveryOptionStructIsMappedToACommand(t *testing.T) {
	t.Parallel()

	declared := declaredOptionStructs(t)
	require.NotEmpty(t, declared, "no option structs found in cmdopts")

	mapped := make([]string, 0, len(commandOptions))
	for _, options := range commandOptions {
		mapped = append(mapped, options.Name())
	}

	slices.Sort(declared)
	slices.Sort(mapped)

	require.Equal(t, declared, mapped,
		"every cmdopts struct must map to a command; update commandOptions in flags.go")
}

func TestEveryMappedCommandIsCanonical(t *testing.T) {
	t.Parallel()

	commands := make([]string, 0, len(earthfile.Commands()))
	for _, cmd := range earthfile.Commands() {
		commands = append(commands, string(cmd))
	}

	for command := range commandOptions {
		require.Contains(t, commands, command,
			"a command mapped to options must be a canonical keyword")
	}
}
