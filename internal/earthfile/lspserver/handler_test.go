package lspserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/servertest"
	"github.com/stretchr/testify/require"
)

func TestHandlerCapabilities(t *testing.T) {
	t.Parallel()

	harness := servertest.New(t, NewHandler("test"))
	require.NotNil(t, harness.InitResult.Capabilities.TextDocumentSync)
	require.NotNil(t, harness.InitResult.Capabilities.HoverProvider)
	require.NotNil(t, harness.InitResult.Capabilities.DefinitionProvider)
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
