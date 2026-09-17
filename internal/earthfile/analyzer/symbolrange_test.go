package analyzer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// symbolBody returns the text a symbol's range covers.
func symbolBody(t *testing.T, doc Document, name string) string {
	t.Helper()

	for _, symbol := range doc.Symbols {
		if symbol.Name == name {
			return doc.Text[symbol.Range.Start:symbol.Range.End]
		}
	}

	t.Fatalf("symbol %q not found", name)

	return ""
}

func TestSymbolRangeCoversTheWholeRecipe(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\n\ndeps:\n\tRUN one\n\tRUN two\n\nall:\n\tBUILD +deps\n"

	doc := Analyze("Earthfile", text)
	require.Empty(t, doc.Diagnostics, "fixture must be canonically valid")

	require.Equal(t, "deps:\n\tRUN one\n\tRUN two", symbolBody(t, doc, "deps"))
	require.Equal(t, "all:\n\tBUILD +deps", symbolBody(t, doc, "all"))
}

func TestSymbolRangeExcludesTheNextDeclarationDocs(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\n\ndeps:\n\tRUN one\n\n# all builds everything.\nall:\n\tBUILD +deps\n"

	doc := Analyze("Earthfile", text)

	body := symbolBody(t, doc, "deps")
	require.Equal(t, "deps:\n\tRUN one", body)
	require.NotContains(t, body, "all builds everything")
}

func TestSymbolRangeOnAnInvalidBuffer(t *testing.T) {
	t.Parallel()

	// NOPE forces the recovery index, which must produce the same body ranges.
	text := "VERSION 0.8\n\ndeps:\n\tNOPE\n\tRUN two\n\nall:\n\tBUILD +deps\n"

	doc := Analyze("Earthfile", text)
	require.NotEmpty(t, doc.Diagnostics, "fixture must be canonically invalid")

	require.Equal(t, "deps:\n\tNOPE\n\tRUN two", symbolBody(t, doc, "deps"))
	require.Equal(t, "all:\n\tBUILD +deps", symbolBody(t, doc, "all"))
}

func TestSymbolRangeOfTheLastDeclarationStopsAtTheEnd(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\n\nall:\n\tRUN one\n\n\n"

	require.Equal(t, "all:\n\tRUN one", symbolBody(t, Analyze("Earthfile", text), "all"))
}

func TestSymbolRangeWithoutATrailingNewline(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\n\nall:\n\tRUN one"

	require.Equal(t, "all:\n\tRUN one", symbolBody(t, Analyze("Earthfile", text), "all"))
}

func TestSymbolRangeContainsSelection(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\n\ndeps:\n\tRUN one\n\nDEPLOY:\n\tFUNCTION\n\tRUN two\n"

	doc := Analyze("Earthfile", text)
	require.Len(t, doc.Symbols, 2)

	for _, symbol := range doc.Symbols {
		require.GreaterOrEqual(t, symbol.Selection.Start, symbol.Range.Start,
			"selection must sit inside the range")
		require.LessOrEqual(t, symbol.Selection.End, symbol.Range.End,
			"selection must sit inside the range")
		require.Equal(t, symbol.Name, doc.Text[symbol.Selection.Start:symbol.Selection.End])
	}

	require.Equal(t, "DEPLOY:\n\tFUNCTION\n\tRUN two", symbolBody(t, doc, "DEPLOY"))
}

func TestSymbolRangesDoNotOverlap(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\n\na:\n\tRUN one\n\nb:\n\tRUN two\n\nc:\n\tRUN three\n"

	doc := Analyze("Earthfile", text)
	require.Len(t, doc.Symbols, 3)

	for i := 1; i < len(doc.Symbols); i++ {
		require.LessOrEqual(t, doc.Symbols[i-1].Range.End, doc.Symbols[i].Range.Start,
			"declaration %d must end before the next begins", i-1)
	}

	require.False(t, strings.Contains(symbolBody(t, doc, "a"), "b:"))
}
