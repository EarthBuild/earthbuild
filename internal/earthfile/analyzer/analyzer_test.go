package analyzer

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type mapLoader map[string]string

func (m mapLoader) Load(path string) (string, error) {
	text, ok := m[filepath.Clean(path)]
	if !ok {
		return "", errors.New("not found")
	}

	return text, nil
}

func TestAnalyzeIndexesDocsImportsAndReferences(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\n" +
		"IMPORT ./lib AS shared\n" +
		"\n" +
		"# build creates the application.\n" +
		"# It uses the shared library.\n" +
		"build:\n" +
		"    BUILD shared+compile\n" +
		"\n" +
		"# COMPILE builds the library.\n" +
		"COMPILE:\n" +
		"    FUNCTION\n" +
		"    RUN true\n"
	doc := Analyze("/workspace/Earthfile", text)
	require.Empty(t, doc.Diagnostics)
	require.Len(t, doc.Symbols, 2)
	require.Equal(t, "build", doc.Symbols[0].Name)
	require.Equal(t, SymbolTarget, doc.Symbols[0].Kind)
	require.Equal(t, "build creates the application.\nIt uses the shared library.", doc.Symbols[0].Docs)
	require.Equal(t, "COMPILE", doc.Symbols[1].Name)
	require.Equal(t, SymbolFunction, doc.Symbols[1].Kind)
	require.Equal(t, "COMPILE builds the library.", doc.Symbols[1].Docs)
	require.Equal(t, "./lib", doc.Imports[0].Path)
	require.Equal(t, "shared", doc.Imports[0].Alias)
	require.Equal(t, "shared+compile", doc.References[0].Raw)
	require.Equal(t, "shared", doc.References[0].Project)
	require.Equal(t, "compile", doc.References[0].Name)
	require.Equal(t, SymbolTarget, doc.References[0].Kind)
}

func TestAnalyzeRemainsNavigableWithParserError(t *testing.T) {
	t.Parallel()

	text := "# docs\nbuild:\n    RUN true\n    NOT_A_COMMAND\n"
	doc := Analyze("/workspace/Earthfile", text)
	require.Len(t, doc.Symbols, 1)
	require.Equal(t, "docs", doc.Symbols[0].Docs)
	require.NotEmpty(t, doc.Diagnostics)
	require.Contains(t, doc.Diagnostics[0].Message, "unknown command")
}

func TestDefinitionAndHoverAcrossLocalImport(t *testing.T) {
	t.Parallel()

	rootPath := filepath.Clean("/workspace/Earthfile")
	libPath := filepath.Clean("/workspace/lib/Earthfile")
	rootText := "VERSION 0.8\nIMPORT ./lib AS shared\napp:\n    BUILD shared+compile\n"
	libText := "VERSION 0.8\n# compile produces the binary.\ncompile:\n    RUN true\n"
	loader := mapLoader{rootPath: rootText, libPath: libText}
	doc := Analyze(rootPath, rootText)
	offset := len(rootText) - len("compile\n")

	location, err := doc.Definition(offset, loader)
	require.NoError(t, err)
	require.Equal(t, libPath, location.Path)
	require.Equal(t, "compile", libText[location.Range.Start:location.Range.End])

	label, docs, ok := doc.Hover(offset, loader)
	require.True(t, ok)
	require.Equal(t, "target +compile", label)
	require.Equal(t, "compile produces the binary.", docs)
}

func TestDefinitionAcrossInlineLocalPath(t *testing.T) {
	t.Parallel()

	rootPath := filepath.Clean("/workspace/Earthfile")
	libPath := filepath.Clean("/workspace/lib/build.earth")
	rootText := "VERSION 0.8\napp:\n    BUILD ./lib+compile\n"
	libText := "VERSION 0.8\ncompile:\n    RUN true\n"
	loader := mapLoader{rootPath: rootText, libPath: libText}
	doc := Analyze(rootPath, rootText)

	location, err := doc.Definition(len(rootText)-len("compile\n"), loader)
	require.NoError(t, err)
	require.Equal(t, libPath, location.Path)
	require.Equal(t, "compile", libText[location.Range.Start:location.Range.End])
}

func TestLocalImportIsScopedToItsTarget(t *testing.T) {
	t.Parallel()

	rootPath := filepath.Clean("/workspace/Earthfile")
	onePath := filepath.Clean("/workspace/one/Earthfile")
	twoPath := filepath.Clean("/workspace/two/Earthfile")
	rootText := "VERSION 0.8\n" +
		"one:\n" +
		"    IMPORT ./one AS lib\n" +
		"    BUILD lib+build\n" +
		"two:\n" +
		"    IMPORT ./two AS lib\n" +
		"    BUILD lib+build\n"
	loader := mapLoader{
		rootPath: rootText,
		onePath:  "VERSION 0.8\nbuild:\n    RUN one\n",
		twoPath:  "VERSION 0.8\n# from two\nbuild:\n    RUN two\n",
	}
	doc := Analyze(rootPath, rootText)
	offset := len(rootText) - len("build\n")

	location, err := doc.Definition(offset, loader)
	require.NoError(t, err)
	require.Equal(t, twoPath, location.Path)
}

func TestValidationDiagnosticUsesTargetLocation(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\nbuild:\n    RUN true\nbuild:\n    RUN false\n"
	doc := Analyze("/workspace/Earthfile", text)
	require.Len(t, doc.Diagnostics, 1)
	require.Equal(t, 3, lineAt(text, doc.Diagnostics[0].Range.Start))
	require.Contains(t, doc.Diagnostics[0].Message, "duplicate target")
}

func lineAt(text string, offset int) int {
	line := 0

	for i := range offset {
		if text[i] == '\n' {
			line++
		}
	}

	return line
}
