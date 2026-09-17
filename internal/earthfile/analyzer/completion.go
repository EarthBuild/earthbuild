package analyzer

import (
	"strings"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// CompletionKind identifies what an editor should offer at a cursor.
type CompletionKind int

const (
	// CompletionNone means the cursor has no completable context.
	CompletionNone CompletionKind = iota
	// CompletionCommand means a command keyword begins at the cursor.
	CompletionCommand
	// CompletionTarget means a target or function name follows a "+".
	CompletionTarget
	// CompletionArtifact means an artifact path follows "+target/".
	CompletionArtifact
	// CompletionFlag means a command option follows "--".
	CompletionFlag
)

// CompletionContext describes what an editor should complete at an offset.
//
// It is derived from the raw line under the cursor rather than the reference
// index, because completion runs mid-token where a reference is not yet
// spelled completely enough to be indexed.
type CompletionContext struct {
	// Kind is the category of completion to offer.
	Kind CompletionKind
	// Prefix is the already-typed text used to filter candidates. It excludes
	// punctuation that introduced the context, such as "+" or "--".
	Prefix string
	// Project is the text before "+" in a target or artifact reference. It is
	// empty for a reference into the current Earthfile, and otherwise holds an
	// IMPORT alias or a relative path.
	Project string
	// Target is the target name in an artifact reference.
	Target string
	// Command is the command keyword on the line, set when the cursor sits in
	// that command's arguments rather than in the keyword itself.
	Command string
	// Scope is the enclosing target or function name, empty in the base recipe.
	Scope string
	// Replace is the byte range a completion item should overwrite. It covers
	// the introducing punctuation for flags so that clients which filter on
	// word boundaries still match.
	Replace Range
}

// CompletionContext classifies the completion opportunity at offset.
func (d Document) CompletionContext(offset int) CompletionContext {
	if offset < 0 || offset > len(d.Text) {
		return CompletionContext{}
	}

	line, ok := d.lineAt(offset)
	if !ok {
		return CompletionContext{}
	}

	column := offset - line.start
	before := line.text[:column]

	if commentStart(before) >= 0 {
		return CompletionContext{Scope: d.scopeAt(line)}
	}

	word, wordStart := wordBefore(before)
	scope := d.scopeAt(line)
	replace := Range{Start: line.start + wordStart, End: offset}

	switch {
	case strings.HasPrefix(word, "--"):
		return CompletionContext{
			Kind:    CompletionFlag,
			Prefix:  word[2:],
			Command: commandBefore(line.text, wordStart),
			Scope:   scope,
			Replace: replace,
		}

	case lastUnescapedPlus(word) >= 0:
		return referenceContext(word, wordStart, line, offset, scope)

	case strings.TrimSpace(before[:wordStart]) == "":
		return CompletionContext{
			Kind:    CompletionCommand,
			Prefix:  word,
			Scope:   scope,
			Replace: replace,
		}
	}

	return CompletionContext{Scope: scope}
}

// referenceContext classifies a word that contains a "+", which is either a
// target reference or, once a "/" follows the target name, an artifact path.
func referenceContext(word string, wordStart int, line sourceLine, offset int, scope string) CompletionContext {
	plus := lastUnescapedPlus(word)
	project := word[:plus]
	after := word[plus+1:]

	if slash := strings.IndexByte(after, '/'); slash >= 0 {
		return CompletionContext{
			Kind:    CompletionArtifact,
			Prefix:  after[slash+1:],
			Project: project,
			Target:  after[:slash],
			Command: commandBefore(line.text, wordStart),
			Scope:   scope,
			Replace: Range{Start: line.start + wordStart + plus + 1 + slash + 1, End: offset},
		}
	}

	return CompletionContext{
		Kind:    CompletionTarget,
		Prefix:  after,
		Project: project,
		Command: commandBefore(line.text, wordStart),
		Scope:   scope,
		Replace: Range{Start: line.start + wordStart + plus + 1, End: offset},
	}
}

// lineAt returns the source line containing offset.
func (d Document) lineAt(offset int) (sourceLine, bool) {
	var found sourceLine

	ok := false

	for _, line := range sourceLines(d.Text) {
		if line.start > offset {
			break
		}

		found = line
		ok = true
	}

	return found, ok
}

// scopeAt reports the target or function enclosing a line. An unindented line
// is either the base recipe or a new declaration, so it has no enclosing
// scope.
func (d Document) scopeAt(line sourceLine) string {
	if indentWidth(line.text) == 0 {
		return ""
	}

	scope := ""

	for _, symbol := range d.Symbols {
		if symbol.Range.Start > line.start {
			break
		}

		scope = symbol.Name
	}

	return scope
}

// commandBefore returns the command keyword spelled before limit on a line,
// joining the two-word keywords such as SAVE ARTIFACT.
func commandBefore(line string, limit int) string {
	var words []string

	for _, token := range tokens(line) {
		if token.start >= limit {
			break
		}

		words = append(words, token.text)
	}

	// WITH introduces a block whose options belong to the command it wraps,
	// as in WITH DOCKER --load.
	if len(words) >= 2 && words[0] == string(earthfile.CmdWith) {
		words = words[1:]
	}

	if len(words) == 0 {
		return ""
	}

	if len(words) >= 2 {
		candidate := words[0] + " " + words[1]
		for _, cmd := range earthfile.MultiWordCommands() {
			if string(cmd) == candidate {
				return candidate
			}
		}
	}

	return words[0]
}

// wordBefore returns the unquoted whitespace-delimited word ending at the end
// of before, plus the column it starts at.
func wordBefore(before string) (string, int) {
	start := len(before)
	for start > 0 && !isWordBoundary(before[start-1]) {
		start--
	}

	return before[start:], start
}

func isWordBoundary(c byte) bool {
	return c == ' ' || c == '\t'
}

func indentWidth(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// commentStart returns the column of the comment marker that begins a
// comment in line, or -1 when the line has no comment before its end.
func commentStart(line string) int {
	var quote byte

	escaped := false

	for i := 0; i < len(line); i++ {
		c := line[i]

		switch {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return i
		}
	}

	return -1
}

// CompletionItemKind identifies the sort of candidate a completion offers.
type CompletionItemKind int

const (
	// ItemKeyword is a canonical Earthfile command keyword.
	ItemKeyword CompletionItemKind = iota + 1
	// ItemTarget is a build target.
	ItemTarget
	// ItemFunction is a reusable FUNCTION or COMMAND.
	ItemFunction
	// ItemFlag is a command option.
	ItemFlag
)

// Completion is an editor-neutral completion candidate.
type Completion struct {
	// Label is the text shown in the completion list.
	Label string
	// Insert is the text written into the buffer over Replace.
	Insert string
	// Detail is a short one-line description, such as a declaration label.
	Detail string
	// Docs is the documentation comment attached to the declaration.
	Docs string
	// Kind categorizes the candidate.
	Kind CompletionItemKind
	// Replace is the byte range the candidate overwrites.
	Replace Range
}

// Completions returns the candidates for the cursor at offset. A nil loader
// restricts results to the current document. Candidates whose project cannot
// be read are omitted rather than reported as an error, because completion
// runs continuously while a user types.
func (d Document) Completions(offset int, loader Loader) []Completion {
	ctx := d.CompletionContext(offset)

	switch ctx.Kind {
	case CompletionCommand:
		return commandCompletions(ctx)
	case CompletionTarget:
		return d.targetCompletions(ctx, loader)
	case CompletionFlag:
		return flagCompletions(ctx)
	case CompletionNone, CompletionArtifact:
		// Artifact candidates need a SAVE ARTIFACT index that the analyzer
		// does not build yet.
		return nil
	}

	return nil
}

func commandCompletions(ctx CompletionContext) []Completion {
	var items []Completion

	for _, cmd := range earthfile.Commands() {
		name := string(cmd)
		if !hasFoldedPrefix(name, ctx.Prefix) {
			continue
		}

		items = append(items, Completion{
			Label:   name,
			Insert:  name,
			Detail:  "command",
			Kind:    ItemKeyword,
			Replace: ctx.Replace,
		})
	}

	return items
}

func (d Document) targetCompletions(ctx CompletionContext, loader Loader) []Completion {
	target, err := d.resolveProject(ctx.Project, ctx.Scope, loader)
	if err != nil || target == nil {
		return nil
	}

	wantTargets, wantFunctions := candidateKindsFor(ctx.Command)

	var items []Completion

	for _, symbol := range target.Symbols {
		if symbol.Kind == SymbolTarget && !wantTargets {
			continue
		}

		if symbol.Kind == SymbolFunction && !wantFunctions {
			continue
		}

		// A declaration cannot usefully reference itself.
		if ctx.Project == "" && symbol.Name == ctx.Scope {
			continue
		}

		if !hasFoldedPrefix(symbol.Name, ctx.Prefix) {
			continue
		}

		kind := ItemTarget
		if symbol.Kind == SymbolFunction {
			kind = ItemFunction
		}

		items = append(items, Completion{
			Label:   symbol.Name,
			Insert:  symbol.Name,
			Detail:  symbol.Label(),
			Docs:    symbol.Docs,
			Kind:    kind,
			Replace: ctx.Replace,
		})
	}

	return items
}

// candidateKindsFor reports which declaration kinds a command accepts. DO
// invokes a function, while the build commands take a target. Anything else,
// including an unrecognized command, accepts both rather than hiding a
// candidate a user meant to pick.
func candidateKindsFor(command string) (targets, functions bool) {
	cmd := earthfile.Cmd(command)

	if cmd == earthfile.CmdDo {
		return false, true
	}

	if cmd == earthfile.CmdBuild || cmd == earthfile.CmdFrom || cmd == earthfile.CmdCopy {
		return true, false
	}

	return true, true
}

// hasFoldedPrefix reports whether value starts with prefix, ignoring case so
// that a lowercase keystroke still matches an uppercase command.
func hasFoldedPrefix(value, prefix string) bool {
	if len(prefix) > len(value) {
		return false
	}

	return strings.EqualFold(value[:len(prefix)], prefix)
}
