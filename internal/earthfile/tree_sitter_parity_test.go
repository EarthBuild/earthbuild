package earthfile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

var treeSitterBoundaryPattern = regexp.MustCompile(
	`(?m)^\s*([0-9]+):[0-9]+\s+-\s+[0-9]+:[0-9]+\s+` +
		`(target|function)\s*$`,
)

//nolint:paralleltest // Tree-sitter compilation is intentionally shared across the fixture subtests.
func TestTreeSitterParity(t *testing.T) {
	treeSitter := os.Getenv("EARTH_TREE_SITTER")
	if treeSitter == "" {
		t.Skip("EARTH_TREE_SITTER is not set; run +tree-sitter-parity")
	}

	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	grammarDir := filepath.Join(repoRoot, "editors", "tree-sitter-earthfile")
	fixtures, err := filepath.Glob(filepath.Join("tests", "*"))
	require.NoError(t, err)

	fixtures = append(fixtures, filepath.Join(repoRoot, "Earthfile"))

	cacheDir := t.TempDir()

	validFixtures := 0

	for _, fixture := range fixtures {
		if filepath.Base(fixture) != "Earthfile" && filepath.Ext(fixture) != ".earth" {
			continue
		}

		text, readErr := os.ReadFile(fixture) // #nosec G304 -- fixture paths are repository-owned.
		require.NoError(t, readErr)

		tree, parseErr := Parse(fixture, string(text), WithSourceMap())
		if parseErr != nil {
			continue
		}

		validFixtures++

		t.Run(filepath.Base(fixture), func(t *testing.T) {
			// #nosec G204,G702 -- both the executable and fixtures belong to this opt-in repository test.
			command := exec.CommandContext(t.Context(), treeSitter, "parse", "--cst", absolutePath(t, fixture))
			command.Dir = grammarDir

			command.Env = append(os.Environ(), "NO_COLOR=1", "XDG_CACHE_HOME="+cacheDir)
			output, commandErr := command.CombinedOutput()
			require.NoError(t, commandErr, string(output))

			require.Equal(t, canonicalBoundaryLines(tree), treeSitterBoundaryLines(string(output)))
		})
	}

	require.Positive(t, validFixtures)
}

func canonicalBoundaryLines(tree Tree) []int {
	lines := make([]int, 0, len(tree.Targets)+len(tree.Functions))

	for _, target := range tree.Targets {
		lines = append(lines, target.SourceLocation.StartLine-1)
	}

	for _, function := range tree.Functions {
		lines = append(lines, function.SourceLocation.StartLine-1)
	}

	slices.Sort(lines)

	return lines
}

func treeSitterBoundaryLines(output string) []int {
	matches := treeSitterBoundaryPattern.FindAllStringSubmatch(output, -1)
	lines := make([]int, 0, len(matches))

	for _, match := range matches {
		line, err := strconv.Atoi(match[1])
		if err != nil {
			panic(fmt.Sprintf("invalid Tree-sitter line %q: %v", match[1], err))
		}

		lines = append(lines, line)
	}

	slices.Sort(lines)

	return lines
}

func absolutePath(t *testing.T, path string) string {
	t.Helper()

	abs, err := filepath.Abs(path)
	require.NoError(t, err)

	return abs
}
