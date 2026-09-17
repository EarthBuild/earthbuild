package lspserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/server"
	"github.com/owenrumney/go-lsp/servertest"
	"github.com/stretchr/testify/require"
)

func TestHandlerCapabilities(t *testing.T) {
	t.Parallel()

	harness := servertest.New(t, NewHandler("test"), servertest.WithServerOptions(
		server.WithSemanticTokensOptions(semanticTokensOptions()),
	))
	require.NotNil(t, harness.InitResult.Capabilities.TextDocumentSync)
	require.NotNil(t, harness.InitResult.Capabilities.HoverProvider)
	require.NotNil(t, harness.InitResult.Capabilities.DefinitionProvider)
	require.NotNil(t, harness.InitResult.Capabilities.SemanticTokensProvider)
	require.Equal(t, semanticTokenTypes, harness.InitResult.Capabilities.SemanticTokensProvider.Legend.TokenTypes)
	require.Equal(t, serverName, harness.InitResult.ServerInfo.Name)
	require.Equal(t, "test", harness.InitResult.ServerInfo.Version)
}

func TestHoverAndDefinitionUseUnsavedImportedDocument(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	rootPath := filepath.Join(tempDir, "Earthfile")
	libPath := filepath.Join(tempDir, "lib", "Earthfile")
	rootURI := pathURI(rootPath)
	libURI := pathURI(libPath)
	rootText := "VERSION 0.8\nIMPORT ./lib AS shared\napp:\n    BUILD shared+compile\n"
	libText := "VERSION 0.8\n# compile produces the binary.\ncompile:\n    RUN true\n"

	harness := servertest.New(t, NewHandler("test"))
	require.NoError(t, harness.DidOpen(libURI, "earth", libText))
	require.NoError(t, harness.DidOpen(rootURI, "earth", rootText))

	hover, err := harness.Hover(rootURI, 3, 19)
	require.NoError(t, err)
	require.NotNil(t, hover)
	require.Contains(t, hover.Contents.Value(), "target +compile")
	require.Contains(t, hover.Contents.Value(), "compile produces the binary.")

	locations, err := harness.Definition(rootURI, 3, 19)
	require.NoError(t, err)
	require.Equal(t, []lsp.Location{{
		URI: libURI,
		Range: lsp.Range{
			Start: lsp.Position{Line: 2, Character: 0},
			End:   lsp.Position{Line: 2, Character: 7},
		},
	}}, locations)
}

func TestDefinitionFindsTargetInParameterizedCopyArtifact(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	rootPath := filepath.Join(tempDir, "packages", "app", "Earthfile")
	monorepoPath := filepath.Join(tempDir, "Earthfile")
	rootURI := pathURI(rootPath)
	monorepoURI := pathURI(monorepoPath)
	rootText := "VERSION 0.8\n" +
		"IMPORT ../../ AS monorepo\n" +
		"test-app:\n" +
		"    COPY (monorepo+compiled-code/packages --scope=\"server\") ./packages\n"
	monorepoText := "VERSION 0.8\n# builds the workspace.\ncompiled-code:\n    RUN true\n"

	harness := servertest.New(t, NewHandler("test"))
	require.NoError(t, harness.DidOpen(monorepoURI, "earth", monorepoText))
	require.NoError(t, harness.DidOpen(rootURI, "earth", rootText))

	locations, err := harness.Definition(rootURI, 3, 25)
	require.NoError(t, err)
	require.Equal(t, []lsp.Location{{
		URI: monorepoURI,
		Range: lsp.Range{
			Start: lsp.Position{Line: 2, Character: 0},
			End:   lsp.Position{Line: 2, Character: 13},
		},
	}}, locations)

	hover, err := harness.Hover(rootURI, 3, 25)
	require.NoError(t, err)
	require.NotNil(t, hover)
	require.Contains(t, hover.Contents.Value(), "builds the workspace.")
}

func TestDiagnosticsPublishOnOpenAndChange(t *testing.T) {
	t.Parallel()

	uri := pathURI(filepath.Join(t.TempDir(), "Earthfile"))
	harness := servertest.New(t, NewHandler("test"))
	require.NoError(t, harness.DidOpen(uri, "earth", "VERSION 0.8\nbuild:\n    INVALID\n"))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	diagnostics, err := harness.WaitForDiagnostics(ctx, uri)
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	require.Equal(t, lsp.SeverityError, *diagnostics[0].Severity)
	require.Contains(t, diagnostics[0].Message, "unknown command")

	harness.ClearDiagnostics()
	require.NoError(t, harness.DidChange(uri, 2, "VERSION 0.8\nbuild:\n    RUN true\n"))
	diagnostics, err = harness.WaitForDiagnostics(ctx, uri)
	require.NoError(t, err)
	require.Empty(t, diagnostics)
}

func TestSemanticTokensContinueAfterNestedShellQuotes(t *testing.T) {
	t.Parallel()

	text := "VERSION 0.8\n" +
		"LET NODE_ARCH=\"$( [ \"$TARGETARCH\" = \"amd64\" ] && echo \"x64\" || echo \"$TARGETARCH\" )\"\n" +
		"LET NODE_URL=\"https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz\"\n" +
		"# compile produces the binary.\n" +
		"compile:\n" +
		"    RUN true\n" +
		"all:\n" +
		"    BUILD +compile\n"
	uri := pathURI(filepath.Join(t.TempDir(), "Earthfile"))
	harness := servertest.New(t, NewHandler("test"), servertest.WithServerOptions(
		server.WithSemanticTokensOptions(semanticTokensOptions()),
	))
	require.NoError(t, harness.DidOpen(uri, "earth", text))

	tokens, err := harness.SemanticTokensFull(uri)
	require.NoError(t, err)
	require.Contains(t, decodedSemanticTokenTexts(text, tokens.Data), "compile")
}

func TestUTF16PositionConversion(t *testing.T) {
	t.Parallel()

	text := "RUN echo \U0001F30D +build\n"
	offset := strings.Index(text, "+build")
	position, err := positionAt(text, offset)
	require.NoError(t, err)
	require.Equal(t, lsp.Position{Line: 0, Character: 12}, position)
}

func TestPathURIRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dir with space", "Earthfile#one")
	uri := pathURI(path)
	got, err := uriPath(uri)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(path), got)
}

func decodedSemanticTokenTexts(text string, data []int) []string {
	line := 0
	character := 0

	lines := strings.Split(text, "\n")
	values := make([]string, 0, len(data)/5)

	for i := 0; i < len(data); i += 5 {
		line += data[i]
		if data[i] == 0 {
			character += data[i+1]
		} else {
			character = data[i+1]
		}

		values = append(values, string([]rune(lines[line])[character:character+data[i+2]]))
	}

	return values
}

func completionLabels(list *lsp.CompletionList) []string {
	labels := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		labels = append(labels, item.Label)
	}

	return labels
}

func TestCompletionAdvertisesTriggerCharacters(t *testing.T) {
	t.Parallel()

	harness := servertest.New(t, NewHandler("test"), servertest.WithServerOptions(
		server.WithCompletionOptions(completionOptions()),
	))
	require.NotNil(t, harness.InitResult.Capabilities.CompletionProvider)
	require.Equal(t, completionTriggerCharacters,
		harness.InitResult.Capabilities.CompletionProvider.TriggerCharacters)
}

func TestCompletionOffersCommandKeywords(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "Earthfile")
	uri := pathURI(path)
	text := "VERSION 0.8\n\nbuild:\n    RU\n"

	harness := servertest.New(t, NewHandler("test"))
	require.NoError(t, harness.DidOpen(uri, "earth", text))

	list, err := harness.Completion(uri, 3, 6)
	require.NoError(t, err)
	require.NotNil(t, list)
	require.Equal(t, []string{"RUN"}, completionLabels(list))

	item := list.Items[0]
	require.NotNil(t, item.Kind)
	require.Equal(t, lsp.CompletionItemKindKeyword, *item.Kind)
	require.NotNil(t, item.TextEdit)
	require.NotNil(t, item.TextEdit.TextEdit)
	require.Equal(t, "RUN", item.TextEdit.TextEdit.NewText)
	require.Equal(t, lsp.Range{
		Start: lsp.Position{Line: 3, Character: 4},
		End:   lsp.Position{Line: 3, Character: 6},
	}, item.TextEdit.TextEdit.Range, "the edit must replace the typed prefix")
}

func TestCompletionOffersTargetsFromAnUnsavedImport(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	rootPath := filepath.Join(tempDir, "Earthfile")
	libPath := filepath.Join(tempDir, "lib", "Earthfile")
	rootURI := pathURI(rootPath)
	rootText := "VERSION 0.8\nIMPORT ./lib AS shared\napp:\n    BUILD shared+\n"
	libText := "VERSION 0.8\n# compile produces the binary.\ncompile:\n    RUN true\n"

	harness := servertest.New(t, NewHandler("test"))
	require.NoError(t, harness.DidOpen(pathURI(libPath), "earth", libText))
	require.NoError(t, harness.DidOpen(rootURI, "earth", rootText))

	list, err := harness.Completion(rootURI, 3, 17)
	require.NoError(t, err)
	require.Equal(t, []string{"compile"}, completionLabels(list))

	item := list.Items[0]
	require.NotNil(t, item.Documentation)
	require.Contains(t, item.Documentation.Value, "compile produces the binary.")
	require.Equal(t, lsp.CompletionItemKindFunction, *item.Kind)
}

func TestCompletionOffersCommandFlags(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "Earthfile")
	uri := pathURI(path)
	text := "VERSION 0.8\n\nbuild:\n    RUN --pu\n"

	harness := servertest.New(t, NewHandler("test"))
	require.NoError(t, harness.DidOpen(uri, "earth", text))

	list, err := harness.Completion(uri, 3, 12)
	require.NoError(t, err)
	require.Equal(t, []string{"--push"}, completionLabels(list))
	require.Equal(t, "--push", list.Items[0].TextEdit.TextEdit.NewText)
	require.Equal(t, lsp.Range{
		Start: lsp.Position{Line: 3, Character: 8},
		End:   lsp.Position{Line: 3, Character: 12},
	}, list.Items[0].TextEdit.TextEdit.Range, "the edit must replace the dashes too")
}

func TestCompletionOnAnInvalidBufferStillAnswers(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "Earthfile")
	uri := pathURI(path)
	// NOPE is not a command, so the canonical parser rejects this buffer.
	text := "VERSION 0.8\n\ndeps:\n    NOPE\n\nall:\n    BUILD +\n"

	harness := servertest.New(t, NewHandler("test"))
	require.NoError(t, harness.DidOpen(uri, "earth", text))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	diagnostics, err := harness.WaitForDiagnostics(ctx, uri)
	require.NoError(t, err)
	require.NotEmpty(t, diagnostics, "the buffer is invalid")

	list, err := harness.Completion(uri, 6, 11)
	require.NoError(t, err)
	require.Equal(t, []string{"deps"}, completionLabels(list))
}

func TestCompletionOutsideAnOpenDocumentIsEmpty(t *testing.T) {
	t.Parallel()

	harness := servertest.New(t, NewHandler("test"))

	list, err := harness.Completion(pathURI(filepath.Join(t.TempDir(), "Earthfile")), 0, 0)
	require.NoError(t, err)
	require.NotNil(t, list)
	require.Empty(t, list.Items)
}

func symbolNames(symbols []lsp.DocumentSymbol) []string {
	names := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		names = append(names, symbol.Name)
	}

	return names
}

func TestDocumentSymbolCapabilityIsAdvertised(t *testing.T) {
	t.Parallel()

	harness := servertest.New(t, NewHandler("test"))
	require.NotNil(t, harness.InitResult.Capabilities.DocumentSymbolProvider)
}

func TestDocumentSymbolListsTargetsAndFunctions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "Earthfile")
	uri := pathURI(path)
	text := "VERSION 0.8\n\n# deps fetches modules.\ndeps:\n    RUN one\n\nDEPLOY:\n    FUNCTION\n    RUN two\n"

	harness := servertest.New(t, NewHandler("test"))
	require.NoError(t, harness.DidOpen(uri, "earth", text))

	symbols, err := harness.DocumentSymbol(uri)
	require.NoError(t, err)
	require.Equal(t, []string{"deps", "DEPLOY"}, symbolNames(symbols))

	deps := symbols[0]
	require.Equal(t, lsp.SymbolKindFunction, deps.Kind)
	require.Equal(t, "target +deps", deps.Detail)
	require.Equal(t, lsp.Range{
		Start: lsp.Position{Line: 3, Character: 0},
		End:   lsp.Position{Line: 4, Character: 11},
	}, deps.Range, "the range must cover the recipe")
	require.Equal(t, lsp.Range{
		Start: lsp.Position{Line: 3, Character: 0},
		End:   lsp.Position{Line: 3, Character: 4},
	}, deps.SelectionRange, "the selection must cover the name")

	require.Equal(t, lsp.SymbolKindMethod, symbols[1].Kind)
	require.Equal(t, "function DEPLOY", symbols[1].Detail)
}

func TestDocumentSymbolSurvivesAnInvalidBuffer(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "Earthfile")
	uri := pathURI(path)
	// An outline must stay populated while a recipe is mid-edit.
	text := "VERSION 0.8\n\ndeps:\n    RUN one\n\nall:\n    BUILD +\n"

	harness := servertest.New(t, NewHandler("test"))
	require.NoError(t, harness.DidOpen(uri, "earth", text))

	symbols, err := harness.DocumentSymbol(uri)
	require.NoError(t, err)
	require.Equal(t, []string{"deps", "all"}, symbolNames(symbols))
}

func TestDocumentSymbolRangesContainTheirSelection(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "Earthfile")
	uri := pathURI(path)
	text := "VERSION 0.8\n\na:\n    RUN one\n\nb:\n    RUN two\n"

	harness := servertest.New(t, NewHandler("test"))
	require.NoError(t, harness.DidOpen(uri, "earth", text))

	symbols, err := harness.DocumentSymbol(uri)
	require.NoError(t, err)
	require.Len(t, symbols, 2)

	for _, symbol := range symbols {
		require.GreaterOrEqual(t, symbol.SelectionRange.Start.Line, symbol.Range.Start.Line)
		require.LessOrEqual(t, symbol.SelectionRange.End.Line, symbol.Range.End.Line)
	}
}

func TestDocumentSymbolOutsideAnOpenDocumentIsEmpty(t *testing.T) {
	t.Parallel()

	harness := servertest.New(t, NewHandler("test"))

	symbols, err := harness.DocumentSymbol(pathURI(filepath.Join(t.TempDir(), "Earthfile")))
	require.NoError(t, err)
	require.Empty(t, symbols)
}
