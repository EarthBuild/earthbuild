// Package lspserver adapts the protocol-neutral Earthfile analyzer to the
// Language Server Protocol.
package lspserver

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/EarthBuild/earthbuild/internal/earthfile/analyzer"
	"github.com/owenrumney/go-lsp/document"
	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/server"
)

const serverName = "earth-lsp"

var semanticTokenTypes = []string{
	"keyword",
	"comment",
	"string",
	"number",
	"operator",
	"parameter",
	"variable",
	"function",
	"namespace",
}

// Handler implements the Earthfile LSP methods.
type Handler struct {
	docs    *document.Store
	client  *server.Client
	version string
}

// NewHandler creates an Earthfile language server handler.
func NewHandler(version string) *Handler {
	return &Handler{
		docs:    document.NewStore(),
		version: version,
	}
}

// Initialize reports server metadata. Capabilities are inferred by go-lsp
// from the optional handler interfaces implemented below.
func (h *Handler) Initialize(context.Context, *lsp.InitializeParams) (*lsp.InitializeResult, error) {
	return &lsp.InitializeResult{
		ServerInfo: &lsp.ServerInfo{Name: serverName, Version: h.version},
	}, nil
}

// Shutdown gracefully stops the language server.
func (h *Handler) Shutdown(context.Context) error {
	return nil
}

// SetClient provides the connection used for diagnostics notifications.
func (h *Handler) SetClient(client *server.Client) {
	h.client = client
}

// DidOpen records a document and immediately publishes diagnostics.
func (h *Handler) DidOpen(ctx context.Context, params *lsp.DidOpenTextDocumentParams) error {
	doc, err := h.docs.Open(params)
	if err != nil {
		return err
	}

	return h.publishDiagnostics(ctx, doc)
}

// DidChange applies incremental edits and publishes updated diagnostics.
func (h *Handler) DidChange(ctx context.Context, params *lsp.DidChangeTextDocumentParams) error {
	doc, err := h.docs.Change(params)
	if err != nil {
		return err
	}

	return h.publishDiagnostics(ctx, doc)
}

// DidClose removes an editor overlay and clears its diagnostics.
func (h *Handler) DidClose(ctx context.Context, params *lsp.DidCloseTextDocumentParams) error {
	h.docs.Close(params)

	if h.client == nil {
		return nil
	}

	return h.client.PublishDiagnostics(ctx, &lsp.PublishDiagnosticsParams{
		URI:         params.TextDocument.URI,
		Diagnostics: []lsp.Diagnostic{},
	})
}

// Hover returns target or function documentation at the cursor.
func (h *Handler) Hover(_ context.Context, params *lsp.HoverParams) (*lsp.Hover, error) {
	doc, source, path, offset, err := h.documentAt(params.TextDocument.URI, params.Position)
	if err != nil || doc == nil {
		return nil, err
	}

	label, docs, ok := analyzer.Analyze(path, source).Hover(offset, h)
	if !ok {
		return nil, nil
	}

	value := "~~~earth\n" + label + "\n~~~"
	if docs != "" {
		value += "\n\n" + docs
	}

	return &lsp.Hover{
		Contents: lsp.NewHoverContents(lsp.Markdown, value),
	}, nil
}

// Definition resolves same-file and local cross-file target and function
// references.
func (h *Handler) Definition(
	_ context.Context,
	params *lsp.DefinitionParams,
) ([]lsp.Location, error) {
	doc, source, path, offset, err := h.documentAt(params.TextDocument.URI, params.Position)
	if err != nil || doc == nil {
		return nil, err
	}

	location, err := analyzer.Analyze(path, source).Definition(offset, h)
	if err != nil {
		// An absent or unreadable local import is not an LSP transport error.
		return nil, nil //nolint:nilerr // A definition can disappear during an edit.
	}

	if location == nil {
		return nil, nil
	}

	targetText, err := h.Load(location.Path)
	if err != nil {
		return nil, nil //nolint:nilerr // The imported file may have moved during the request.
	}

	targetRange, err := protocolRange(targetText, location.Range)
	if err != nil {
		return nil, err
	}

	return []lsp.Location{{
		URI:   pathURI(location.Path),
		Range: targetRange,
	}}, nil
}

// SemanticTokensFull returns semantic highlighting derived from the canonical
// Earthfile lexer and the protocol-neutral semantic index.
func (h *Handler) SemanticTokensFull(
	_ context.Context,
	params *lsp.SemanticTokensParams,
) (*lsp.SemanticTokens, error) {
	doc, ok := h.docs.Get(params.TextDocument.URI)
	if !ok {
		return &lsp.SemanticTokens{Data: []int{}}, nil
	}

	path, err := uriPath(params.TextDocument.URI)
	if err != nil {
		return nil, err
	}

	data, err := encodeSemanticTokens(doc.Text(), analyzer.Analyze(path, doc.Text()).SemanticTokens())
	if err != nil {
		return nil, err
	}

	return &lsp.SemanticTokens{Data: data}, nil
}

// Load implements analyzer.Loader, preferring unsaved open buffers to files
// on disk.
func (h *Handler) Load(path string) (string, error) {
	if text, ok := h.docs.Text(pathURI(path)); ok {
		return text, nil
	}

	contents, err := os.ReadFile(path) // #nosec G304 -- local paths come from the user's Earthfile.
	if err != nil {
		return "", err
	}

	return string(contents), nil
}

func (h *Handler) documentAt(
	uri lsp.DocumentURI,
	position lsp.Position,
) (*document.Document, string, string, int, error) {
	doc, ok := h.docs.Get(uri)
	if !ok {
		return nil, "", "", 0, nil
	}

	path, err := uriPath(uri)
	if err != nil {
		return nil, "", "", 0, err
	}

	offset, err := doc.OffsetAt(position)
	if err != nil {
		return nil, "", "", 0, err
	}

	return doc, doc.Text(), path, offset, nil
}

func (h *Handler) publishDiagnostics(ctx context.Context, doc *document.Document) error {
	if h.client == nil {
		return nil
	}

	path, err := uriPath(doc.URI())
	if err != nil {
		return err
	}

	analysis := analyzer.Analyze(path, doc.Text())
	severity := lsp.SeverityError

	diagnostics := make([]lsp.Diagnostic, 0, len(analysis.Diagnostics))
	for _, diagnostic := range analysis.Diagnostics {
		rng, rangeErr := protocolRange(doc.Text(), diagnostic.Range)
		if rangeErr != nil {
			return rangeErr
		}

		diagnostics = append(diagnostics, lsp.Diagnostic{
			Range:    rng,
			Severity: &severity,
			Source:   "earth",
			Message:  diagnostic.Message,
		})
	}

	version := doc.Version()

	return h.client.PublishDiagnostics(ctx, &lsp.PublishDiagnosticsParams{
		URI:         doc.URI(),
		Version:     &version,
		Diagnostics: diagnostics,
	})
}

func protocolRange(text string, sourceRange analyzer.Range) (lsp.Range, error) {
	start, err := positionAt(text, sourceRange.Start)
	if err != nil {
		return lsp.Range{}, err
	}

	end, err := positionAt(text, sourceRange.End)
	if err != nil {
		return lsp.Range{}, err
	}

	return lsp.Range{Start: start, End: end}, nil
}

func encodeSemanticTokens(text string, tokens []analyzer.SemanticToken) ([]int, error) {
	data := make([]int, 0, len(tokens)*5)
	previous := lsp.Position{}

	for _, token := range tokens {
		for _, segment := range splitSemanticRange(text, token.Range) {
			start, err := positionAt(text, segment.Start)
			if err != nil {
				return nil, err
			}

			length := utf16Length(text[segment.Start:segment.End])
			if length == 0 {
				continue
			}

			deltaLine := start.Line - previous.Line

			deltaStart := start.Character
			if deltaLine == 0 {
				deltaStart -= previous.Character
			}

			modifiers := 0
			if token.Declaration {
				modifiers = 1
			}

			data = append(data, deltaLine, deltaStart, length, semanticTokenIndex(token.Kind), modifiers)
			previous = start
		}
	}

	return data, nil
}

func splitSemanticRange(text string, sourceRange analyzer.Range) []analyzer.Range {
	var ranges []analyzer.Range

	for start := sourceRange.Start; start < sourceRange.End; {
		end := sourceRange.End
		if newline := strings.IndexByte(text[start:end], '\n'); newline >= 0 {
			end = start + newline
		}

		if end > start && text[end-1] == '\r' {
			end--
		}

		if end > start {
			ranges = append(ranges, analyzer.Range{Start: start, End: end})
		}

		if end >= sourceRange.End {
			break
		}

		start += strings.IndexByte(text[start:sourceRange.End], '\n') + 1
	}

	return ranges
}

func utf16Length(text string) int {
	length := 0
	for _, r := range text {
		length += utf16.RuneLen(r)
	}

	return length
}

func semanticTokenIndex(kind analyzer.SemanticKind) int {
	return int(kind) - 1
}

func positionAt(text string, offset int) (lsp.Position, error) {
	if offset < 0 || offset > len(text) {
		return lsp.Position{}, fmt.Errorf("earth lsp: byte offset %d is outside document", offset)
	}

	if !utf8.ValidString(text[:offset]) {
		return lsp.Position{}, fmt.Errorf("earth lsp: byte offset %d splits a UTF-8 sequence", offset)
	}

	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	line := strings.Count(text[:lineStart], "\n")

	character := 0
	for _, r := range text[lineStart:offset] {
		character += utf16.RuneLen(r)
	}

	return lsp.Position{Line: line, Character: character}, nil
}

func uriPath(uri lsp.DocumentURI) (string, error) {
	parsed, err := url.Parse(string(uri))
	if err != nil {
		return "", fmt.Errorf("earth lsp: parse document URI: %w", err)
	}

	if parsed.Scheme != "file" {
		return "", fmt.Errorf("earth lsp: unsupported document URI scheme %q", parsed.Scheme)
	}

	path := parsed.Path
	if parsed.Host != "" && parsed.Host != "localhost" {
		path = "//" + parsed.Host + path
	}

	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}

	return filepath.Clean(filepath.FromSlash(path)), nil
}

func pathURI(path string) lsp.DocumentURI {
	path = filepath.Clean(path)

	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}

	return lsp.DocumentURI((&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
}
