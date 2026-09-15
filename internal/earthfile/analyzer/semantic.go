package analyzer

import (
	"sort"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// SemanticKind identifies an editor-neutral semantic token class.
type SemanticKind int

const (
	// SemanticKeyword identifies an Earthfile command keyword.
	SemanticKeyword SemanticKind = iota + 1
	// SemanticComment identifies a comment.
	SemanticComment
	// SemanticString identifies a quoted argument.
	SemanticString
	// SemanticNumber identifies a numeric argument.
	SemanticNumber
	// SemanticOperator identifies an operator.
	SemanticOperator
	// SemanticParameter identifies a command option.
	SemanticParameter
	// SemanticVariable identifies a variable reference.
	SemanticVariable
	// SemanticFunction identifies a target or function declaration or reference.
	SemanticFunction
	// SemanticNamespace identifies an imported Earthfile path.
	SemanticNamespace
)

// SemanticToken is a half-open byte range with editor-facing meaning.
type SemanticToken struct {
	Range       Range
	Kind        SemanticKind
	Declaration bool
}

// SemanticTokens returns non-overlapping tokens derived from the canonical
// lexer plus the analyzer's target, function, reference, and import index.
func (d Document) SemanticTokens() []SemanticToken {
	structural := d.structuralSemanticTokens()
	tokens := append([]SemanticToken(nil), structural...)

	for _, token := range earthfile.SyntaxTokens(d.Path, d.Text) {
		candidate := SemanticToken{
			Range: Range{Start: token.Start, End: token.End},
			Kind:  semanticKind(token.Kind),
		}
		if !overlapsAny(candidate.Range, structural) {
			tokens = append(tokens, candidate)
		}
	}

	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].Range.Start == tokens[j].Range.Start {
			return tokens[i].Range.End < tokens[j].Range.End
		}

		return tokens[i].Range.Start < tokens[j].Range.Start
	})

	return tokens
}

func (d Document) structuralSemanticTokens() []SemanticToken {
	tokens := make([]SemanticToken, 0, len(d.Symbols)+len(d.Imports)+len(d.References))

	for _, symbol := range d.Symbols {
		tokens = append(tokens, SemanticToken{
			Range:       symbol.Selection,
			Kind:        SemanticFunction,
			Declaration: true,
		})
	}

	for _, imp := range d.Imports {
		tokens = append(tokens, SemanticToken{Range: imp.PathRange, Kind: SemanticNamespace})
	}

	for _, ref := range d.References {
		tokens = append(tokens, SemanticToken{Range: ref.Range, Kind: SemanticFunction})
	}

	return tokens
}

func semanticKind(kind earthfile.SyntaxTokenKind) SemanticKind {
	switch kind {
	case earthfile.SyntaxKeyword:
		return SemanticKeyword
	case earthfile.SyntaxComment:
		return SemanticComment
	case earthfile.SyntaxString:
		return SemanticString
	case earthfile.SyntaxNumber:
		return SemanticNumber
	case earthfile.SyntaxOperator:
		return SemanticOperator
	case earthfile.SyntaxParameter:
		return SemanticParameter
	case earthfile.SyntaxVariable:
		return SemanticVariable
	}

	return SemanticVariable
}

func overlapsAny(rng Range, tokens []SemanticToken) bool {
	for _, token := range tokens {
		if rng.Start < token.Range.End && token.Range.Start < rng.End {
			return true
		}
	}

	return false
}
