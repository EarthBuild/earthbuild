package earthfile

import "fmt"

// SourceTokenKind identifies the role of a lossless canonical lexer token.
type SourceTokenKind int

const (
	// SourceTokenOther identifies a token not otherwise classified for tooling.
	SourceTokenOther SourceTokenKind = iota
	// SourceTokenKeyword identifies an Earthfile command or control keyword.
	SourceTokenKeyword
	// SourceTokenArgument identifies an argument atom exactly as written.
	SourceTokenArgument
	// SourceTokenTarget identifies a target declaration token.
	SourceTokenTarget
	// SourceTokenFunction identifies a function declaration token.
	SourceTokenFunction
	// SourceTokenComment identifies a full-line or trailing comment.
	SourceTokenComment
	// SourceTokenWhitespace identifies horizontal whitespace.
	SourceTokenWhitespace
	// SourceTokenNewline identifies a physical newline.
	SourceTokenNewline
	// SourceTokenOperator identifies an assignment operator.
	SourceTokenOperator
)

// SourceToken is a canonical lexer token with its original byte and source
// coordinates. Line and Column are one-based.
type SourceToken struct {
	Text   string
	Start  int
	End    int
	Line   int
	Column int
	Kind   SourceTokenKind
}

// SourceTokens returns the canonical lexer's lossless token stream. Tokens
// preceding a lexical error are returned together with the error.
func SourceTokens(name, text string) ([]SourceToken, error) {
	lexer := lex(name, text)
	tokens := make([]SourceToken, 0, 64)

	for {
		lexItem := lexer.nextItem()
		//nolint:exhaustive // EOF and errors terminate; every other token is preserved.
		switch lexItem.Typ {
		case itemEOF:
			return tokens, nil
		case itemError:
			return tokens, fmt.Errorf("earthfile: lexical error at byte %d: %s", lexItem.pos, lexItem.Val)
		default:
			tokens = append(tokens, SourceToken{
				Text:   lexItem.Val,
				Start:  int(lexItem.pos),
				End:    int(lexItem.pos) + len(lexItem.Val),
				Line:   lexItem.Line,
				Column: lexItem.Col,
				Kind:   sourceTokenKind(lexItem.Typ),
			})
		}
	}
}

func sourceTokenKind(typ itemType) SourceTokenKind {
	switch {
	case isKeywordItem(typ):
		return SourceTokenKeyword
	case typ == itemAtom:
		return SourceTokenArgument
	case typ == itemTarget:
		return SourceTokenTarget
	case typ == itemFunction || typ == itemUserCommand:
		return SourceTokenFunction
	case typ == itemComment || typ == itemEOLComment:
		return SourceTokenComment
	case typ == itemWS:
		return SourceTokenWhitespace
	case typ == itemNL:
		return SourceTokenNewline
	case typ == itemEquals:
		return SourceTokenOperator
	default:
		return SourceTokenOther
	}
}
