// Package analyzer provides editor-oriented analysis of Earthfile source text.
//
// The analyzer deliberately keeps byte offsets and filesystem access independent
// of any editor protocol. Native LSP and future WebAssembly hosts can therefore
// share the same indexing and resolution behavior.
package analyzer

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

var (
	declarationPattern        = regexp.MustCompile(`^([a-z][a-zA-Z0-9.\-]*|[A-Z][A-Z0-9._]*):`)
	explicitFunctionPattern   = regexp.MustCompile(`^(?:COMMAND|FUNCTION)[ \t]+([A-Z][A-Z0-9._]*):`)
	importPattern             = regexp.MustCompile(`^IMPORT[ \t]+([^ \t#]+)(?:[ \t]+AS[ \t]+([^ \t#]+))?`)
	commandPattern            = regexp.MustCompile(`^(?:BUILD|FROM|COPY|DO)(?:[ \t]|$)`)
	parserPositionPattern     = regexp.MustCompile("syntax error at pos ([0-9]+):")
	validationPositionPattern = regexp.MustCompile(`line ([0-9]+):([0-9]+) ([^\n]+)`)
)

// SymbolKind identifies an Earthfile declaration.
type SymbolKind int

const (
	// SymbolTarget is a build target declaration.
	SymbolTarget SymbolKind = iota + 1
	// SymbolFunction is a reusable COMMAND or FUNCTION declaration.
	SymbolFunction
)

// Range is a half-open byte range in source text.
type Range struct {
	Start int
	End   int
}

// Contains reports whether offset is inside the half-open range.
func (r Range) Contains(offset int) bool {
	return offset >= r.Start && offset < r.End
}

// Symbol describes a target or function declaration.
type Symbol struct {
	Name      string
	Docs      string
	Scope     string
	Range     Range
	Selection Range
	Kind      SymbolKind
}

// Import describes an IMPORT declaration.
type Import struct {
	Path      string
	Alias     string
	Scope     string
	Range     Range
	PathRange Range
}

// Reference describes a target or function reference.
type Reference struct {
	Raw     string
	Project string
	Name    string
	Scope   string
	Range   Range
	Kind    SymbolKind
}

// Diagnostic is an editor-neutral source diagnostic.
type Diagnostic struct {
	Message string
	Range   Range
}

// Document is the analysis result for one Earthfile.
type Document struct {
	Path        string
	Text        string
	Symbols     []Symbol
	Imports     []Import
	References  []Reference
	Diagnostics []Diagnostic
}

// Location identifies a byte range in an Earthfile.
type Location struct {
	Path  string
	Range Range
}

// Loader supplies Earthfile text to cross-file operations. A host can overlay
// unsaved editor buffers before falling back to disk.
type Loader interface {
	Load(path string) (string, error)
}

// Analyze creates a tolerant semantic index and runs the canonical parser for
// diagnostics. Indexing is line based so navigation remains available while a
// user is in the middle of an invalid edit.
func Analyze(path, text string) Document {
	cleanPath := filepath.Clean(path)

	tree, parseErr := earthfile.Parse(cleanPath, text, earthfile.WithSourceMap())
	if parseErr == nil {
		tokens, tokenErr := earthfile.SourceTokens(cleanPath, text)
		if tokenErr == nil {
			return analyzeCanonical(cleanPath, text, tree, tokens)
		}

		return analyzeRecovery(cleanPath, text)
	}

	doc := analyzeRecovery(cleanPath, text)
	doc.Diagnostics = parserDiagnostics(text, parseErr)

	return doc
}

func analyzeRecovery(path, text string) Document {
	doc := Document{Path: filepath.Clean(path), Text: text}
	lines := sourceLines(text)
	currentScope := ""

	var pendingDocs []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line.text)
		if trimmed == "" {
			pendingDocs = nil
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			if strings.HasPrefix(line.text, "#") {
				pendingDocs = append(pendingDocs, commentText(trimmed))
			} else {
				pendingDocs = nil
			}

			continue
		}

		indent := len(line.text) - len(strings.TrimLeft(line.text, " \\t"))

		body := line.text[indent:]
		if indent == 0 {
			if symbol, ok := parseDeclaration(body, line.start, pendingDocs); ok {
				doc.Symbols = append(doc.Symbols, symbol)
				currentScope = symbol.Name
				pendingDocs = nil

				continue
			}

			currentScope = ""
		}

		if imp, ok := parseImport(body, line.start+indent, currentScope); ok {
			doc.Imports = append(doc.Imports, imp)
		}

		if commandPattern.MatchString(body) {
			doc.References = append(doc.References, parseReferences(body, line.start+indent, currentScope)...)
		}

		pendingDocs = nil
	}

	extendSymbolRanges(text, doc.Symbols)

	return doc
}

// Definition resolves the declaration under offset, including references
// reached through local IMPORT aliases or inline local paths.
func (d Document) Definition(offset int, loader Loader) (*Location, error) {
	for _, symbol := range d.Symbols {
		if symbol.Selection.Contains(offset) {
			return &Location{Path: d.Path, Range: symbol.Selection}, nil
		}
	}

	for _, imp := range d.Imports {
		if imp.PathRange.Contains(offset) && isLocalProject(imp.Path) {
			path, _, err := loadProject(d.Path, imp.Path, loader)
			if err != nil {
				return nil, err
			}

			return &Location{Path: path, Range: Range{}}, nil
		}
	}

	ref := d.referenceAt(offset)
	if ref == nil {
		return nil, nil
	}

	targetDoc, err := d.resolveReference(*ref, loader)
	if err != nil || targetDoc == nil {
		return nil, err
	}

	for _, symbol := range targetDoc.Symbols {
		if symbol.Name == ref.Name && symbol.Kind == ref.Kind {
			return &Location{Path: targetDoc.Path, Range: symbol.Selection}, nil
		}
	}

	return nil, nil
}

// Hover returns the declaration label and documentation for the item under
// offset. The boolean is false when the offset has no resolvable declaration.
func (d Document) Hover(offset int, loader Loader) (string, string, bool) {
	for _, symbol := range d.Symbols {
		if symbol.Selection.Contains(offset) {
			return symbolLabel(symbol), symbol.Docs, true
		}
	}

	ref := d.referenceAt(offset)
	if ref == nil {
		return "", "", false
	}

	targetDoc, err := d.resolveReference(*ref, loader)
	if err != nil || targetDoc == nil {
		return "", "", false
	}

	for _, symbol := range targetDoc.Symbols {
		if symbol.Name == ref.Name && symbol.Kind == ref.Kind {
			return symbolLabel(symbol), symbol.Docs, true
		}
	}

	return "", "", false
}

func (d Document) referenceAt(offset int) *Reference {
	for i := range d.References {
		if d.References[i].Range.Contains(offset) {
			return &d.References[i]
		}
	}

	return nil
}

func (d Document) resolveReference(ref Reference, loader Loader) (*Document, error) {
	return d.resolveProject(ref.Project, ref.Scope, loader)
}

// resolveProject analyzes the Earthfile a reference prefix names, resolving an
// IMPORT alias visible in scope before falling back to treating the prefix as
// a path. It returns a nil document when the project is not local, which a
// workspace cannot read.
func (d Document) resolveProject(project, scope string, loader Loader) (*Document, error) {
	if project == "" {
		return &d, nil
	}

	if !isLocalProject(project) {
		for _, imp := range slices.Backward(d.Imports) {
			if imp.Alias == project && (imp.Scope == "" || imp.Scope == scope) {
				project = imp.Path
				break
			}
		}
	}

	if !isLocalProject(project) {
		return nil, nil
	}

	path, text, err := loadProject(d.Path, project, loader)
	if err != nil {
		return nil, err
	}

	doc := Analyze(path, text)

	return &doc, nil
}

func parseDeclaration(body string, lineStart int, docs []string) (Symbol, bool) {
	kind := SymbolTarget

	match := declarationPattern.FindStringSubmatchIndex(body)
	if match == nil {
		match = explicitFunctionPattern.FindStringSubmatchIndex(body)
		kind = SymbolFunction
	}

	if match == nil {
		return Symbol{}, false
	}

	nameStart, nameEnd := match[2], match[3]

	name := body[nameStart:nameEnd]
	if name[0] >= 'A' && name[0] <= 'Z' {
		kind = SymbolFunction
	}

	selection := Range{Start: lineStart + nameStart, End: lineStart + nameEnd}

	return Symbol{
		Name:      name,
		Kind:      kind,
		Range:     Range{Start: lineStart, End: lineStart + match[1]},
		Selection: selection,
		Docs:      strings.TrimSpace(strings.Join(docs, "\n")),
		Scope:     name,
	}, true
}

func parseImport(body string, start int, scope string) (Import, bool) {
	match := importPattern.FindStringSubmatchIndex(body)
	if match == nil {
		return Import{}, false
	}

	path := unquote(body[match[2]:match[3]])

	alias := ""
	if match[4] >= 0 {
		alias = unquote(body[match[4]:match[5]])
	}

	if alias == "" {
		alias = filepath.Base(filepath.Clean(path))
	}

	return Import{
		Path:      path,
		Alias:     alias,
		Range:     Range{Start: start + match[0], End: start + match[1]},
		PathRange: Range{Start: start + match[2], End: start + match[3]},
		Scope:     scope,
	}, true
}

func parseReferences(body string, start int, scope string) []Reference {
	var refs []Reference

	for _, token := range tokens(body) {
		raw := unquote(token.text)

		plus := lastUnescapedPlus(raw)
		if plus < 0 || plus == len(raw)-1 {
			continue
		}

		nameEnd := strings.IndexByte(raw[plus+1:], '/')
		if nameEnd < 0 {
			nameEnd = len(raw)
		} else {
			nameEnd += plus + 1
		}

		candidate := raw[:nameEnd]
		name := raw[plus+1 : nameEnd]
		kind := SymbolTarget

		_, targetErr := domain.ParseTarget(candidate)
		if targetErr != nil {
			_, commandErr := domain.ParseCommand(candidate)
			if commandErr != nil {
				continue
			}

			kind = SymbolFunction
		}

		leadingQuote := len(token.text) - len(strings.TrimLeft(token.text, "\"'"))
		refs = append(refs, Reference{
			Raw:     candidate,
			Project: raw[:plus],
			Name:    name,
			Kind:    kind,
			Range: Range{
				Start: start + token.start + leadingQuote,
				End:   start + token.start + leadingQuote + len(candidate),
			},
			Scope: scope,
		})
	}

	return refs
}

func loadProject(fromPath, project string, loader Loader) (string, string, error) {
	if loader == nil {
		return "", "", errors.New("analyzer: no document loader configured")
	}

	projectPath := project
	if !filepath.IsAbs(projectPath) {
		projectPath = filepath.Join(filepath.Dir(fromPath), filepath.FromSlash(projectPath))
	}

	projectPath = filepath.Clean(projectPath)

	candidates := []string{projectPath}
	if filepath.Base(projectPath) != "Earthfile" && !strings.HasSuffix(projectPath, ".earth") {
		candidates = []string{
			filepath.Join(projectPath, "Earthfile"),
			filepath.Join(projectPath, "build.earth"),
		}
	}

	var errs []error

	for _, candidate := range candidates {
		text, err := loader.Load(candidate)
		if err == nil {
			return candidate, text, nil
		}

		errs = append(errs, err)
	}

	return "", "", fmt.Errorf("load local Earthfile %q: %w", project, errors.Join(errs...))
}

func parserDiagnostics(text string, err error) []Diagnostic {
	message := err.Error()
	if matches := validationPositionPattern.FindAllStringSubmatch(message, -1); len(matches) > 0 {
		diagnostics := make([]Diagnostic, 0, len(matches))
		for _, match := range matches {
			line, _ := strconv.Atoi(match[1])
			column, _ := strconv.Atoi(match[2])
			start := offsetForLineColumn(text, line, column)
			diagnostics = append(diagnostics, Diagnostic{
				Range:   characterRange(text, start),
				Message: strings.TrimSpace(match[3]),
			})
		}

		return diagnostics
	}

	start := 0
	if match := parserPositionPattern.FindStringSubmatch(message); match != nil {
		start, _ = strconv.Atoi(match[1])
		start = min(max(start, 0), len(text))
	}

	return []Diagnostic{{Range: characterRange(text, start), Message: message}}
}

func characterRange(text string, start int) Range {
	end := start
	if start < len(text) {
		_, width := utf8.DecodeRuneInString(text[start:])
		end += width
	}

	return Range{Start: start, End: end}
}

func offsetForLineColumn(text string, wantedLine, wantedColumn int) int {
	line, column := 1, 1
	for offset, r := range text {
		if line == wantedLine && column == wantedColumn {
			return offset
		}

		if r == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}

	return len(text)
}

func symbolLabel(symbol Symbol) string {
	if symbol.Kind == SymbolFunction {
		return "function " + symbol.Name
	}

	return "target +" + symbol.Name
}

func isLocalProject(project string) bool {
	return strings.HasPrefix(project, ".") || filepath.IsAbs(project)
}

func commentText(line string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
}

func unquote(value string) string {
	return strings.Trim(value, "\"'")
}

func lastUnescapedPlus(value string) int {
	for i := len(value) - 1; i >= 0; i-- {
		if value[i] != '+' {
			continue
		}

		backslashes := 0
		for j := i - 1; j >= 0 && value[j] == '\\'; j-- {
			backslashes++
		}

		if backslashes%2 == 0 {
			return i
		}
	}

	return -1
}

type sourceLine struct {
	text  string
	start int
}

func sourceLines(text string) []sourceLine {
	lines := make([]sourceLine, 0, strings.Count(text, "\n")+1)

	start := 0
	for start <= len(text) {
		end := strings.IndexByte(text[start:], '\n')
		if end < 0 {
			lines = append(lines, sourceLine{text: strings.TrimSuffix(text[start:], "\r"), start: start})
			break
		}

		end += start
		lines = append(lines, sourceLine{text: strings.TrimSuffix(text[start:end], "\r"), start: start})
		start = end + 1
	}

	return lines
}

type sourceToken struct {
	text  string
	start int
}

func tokens(line string) []sourceToken {
	var result []sourceToken

	for i := 0; i < len(line); {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}

		if i >= len(line) || line[i] == '#' {
			break
		}

		start := i

		var quote byte

		escaped := false

		for i < len(line) {
			c := line[i]

			if escaped {
				escaped = false
				i++

				continue
			}

			if c == '\\' {
				escaped = true
				i++

				continue
			}

			if quote != 0 {
				if c == quote {
					quote = 0
				}

				i++

				continue
			}

			if c == '\'' || c == '"' {
				quote = c
				i++

				continue
			}

			if c == ' ' || c == '\t' || c == '#' {
				break
			}

			i++
		}

		result = append(result, sourceToken{text: line[start:i], start: start})
		if i < len(line) && line[i] == '#' {
			break
		}
	}

	return result
}

// extendSymbolRanges grows each declaration's range from its header line to
// the whole recipe, so that an editor outline can nest commands under the
// target they belong to and track the enclosing declaration while scrolling.
//
// Symbols must be ordered by declaration position, which both the canonical
// and the recovery index guarantee.
func extendSymbolRanges(text string, symbols []Symbol) {
	if len(symbols) == 0 {
		return
	}

	lines := sourceLines(text)

	for i := range symbols {
		boundary := len(text)
		if i+1 < len(symbols) {
			boundary = symbols[i+1].Range.Start
		}

		if end := recipeEnd(lines, symbols[i].Range.End, boundary); end > symbols[i].Range.End {
			symbols[i].Range.End = end
		}
	}
}

// recipeEnd returns the end of the last line of recipe body before boundary.
// Blank lines and unindented comments are excluded because they introduce the
// next declaration rather than closing the current one.
func recipeEnd(lines []sourceLine, min, boundary int) int {
	end := min

	for _, line := range lines {
		if line.start >= boundary {
			break
		}

		if strings.TrimSpace(line.text) == "" || strings.HasPrefix(line.text, "#") {
			continue
		}

		if lineEnd := line.start + len(line.text); lineEnd > end {
			end = lineEnd
		}
	}

	return end
}
