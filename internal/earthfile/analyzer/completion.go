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
