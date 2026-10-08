package earthfile

import (
	"strings"
	"unicode"
)

// SyntaxTokenKind identifies editor-facing lexical syntax classes emitted by
// the canonical Earthfile lexer.
type SyntaxTokenKind int

const (
	// SyntaxKeyword identifies an Earthfile command keyword.
	SyntaxKeyword SyntaxTokenKind = iota + 1
	// SyntaxComment identifies a full-line or trailing comment.
	SyntaxComment
	// SyntaxString identifies a quoted argument.
	SyntaxString
	// SyntaxNumber identifies a numeric argument.
	SyntaxNumber
	// SyntaxOperator identifies an assignment operator.
	SyntaxOperator
	// SyntaxParameter identifies a command-line option.
	SyntaxParameter
	// SyntaxVariable identifies a variable reference.
	SyntaxVariable
)

// SyntaxToken is a half-open byte range classified by the canonical lexer.
type SyntaxToken struct {
	Start int
	End   int
	Kind  SyntaxTokenKind
}

// SyntaxTokens lexes text with the canonical Earthfile lexer and returns the
// editor-facing tokens produced before EOF or the first lexical error.
func SyntaxTokens(name, text string) []SyntaxToken {
	sourceTokens, _ := SourceTokens(name, text)

	var tokens []SyntaxToken

	for _, sourceToken := range sourceTokens {
		switch sourceToken.Kind {
		case SourceTokenKeyword:
			tokens = append(tokens, SyntaxToken{
				Start: sourceToken.Start,
				End:   sourceToken.End,
				Kind:  SyntaxKeyword,
			})
		case SourceTokenComment:
			tokens = append(tokens, SyntaxToken{
				Start: sourceToken.Start,
				End:   sourceToken.End,
				Kind:  SyntaxComment,
			})
		case SourceTokenOperator:
			tokens = append(tokens, SyntaxToken{
				Start: sourceToken.Start,
				End:   sourceToken.End,
				Kind:  SyntaxOperator,
			})
		case SourceTokenArgument:
			tokens = append(tokens, classifyAtom(sourceToken.Text, sourceToken.Start)...)
		case SourceTokenOther, SourceTokenTarget, SourceTokenFunction,
			SourceTokenWhitespace, SourceTokenNewline:
			// Structural tokens are classified by the analyzer when appropriate.
		}
	}

	return tokens
}

func isKeywordItem(typ itemType) bool {
	return typ >= itemFrom && typ <= itemWait
}

func classifyAtom(value string, start int) []SyntaxToken {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return []SyntaxToken{{Start: start, End: start + len(value), Kind: SyntaxString}}
	}

	if strings.HasPrefix(value, "--") {
		end := strings.IndexByte(value, '=')
		if end < 0 {
			end = len(value)
		}

		tokens := []SyntaxToken{{Start: start, End: start + end, Kind: SyntaxParameter}}
		if end < len(value) {
			tokens = append(tokens, variableTokens(value[end+1:], start+end+1)...)
		}

		return tokens
	}

	if isNumber(value) {
		return []SyntaxToken{{Start: start, End: start + len(value), Kind: SyntaxNumber}}
	}

	return variableTokens(value, start)
}

func isNumber(value string) bool {
	if value == "" {
		return false
	}

	for _, r := range value {
		if !unicode.IsDigit(r) && r != '.' {
			return false
		}
	}

	return true
}

func variableTokens(value string, start int) []SyntaxToken {
	var tokens []SyntaxToken

	for i := 0; i < len(value); i++ {
		if value[i] != '$' || i+1 >= len(value) || value[i+1] == '(' {
			continue
		}

		nameStart := i + 1

		nameEnd := nameStart
		if value[nameStart] == '{' {
			nameStart++
			nameEnd = nameStart
		}

		for nameEnd < len(value) && isVariableByte(value[nameEnd]) {
			nameEnd++
		}

		if nameEnd > nameStart {
			tokens = append(tokens, SyntaxToken{
				Start: start + nameStart,
				End:   start + nameEnd,
				Kind:  SyntaxVariable,
			})
		}

		i = nameEnd - 1
	}

	return tokens
}

func isVariableByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}
