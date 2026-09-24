package analyzer

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/util/flagutil"
)

type scopedCommand struct {
	scope   string
	command earthfile.Command
}

func analyzeCanonical(
	path string,
	text string,
	tree earthfile.Tree,
	tokens []earthfile.SourceToken,
) Document {
	doc := Document{Path: path, Text: text}

	for _, target := range tree.Targets {
		if symbol, ok := canonicalSymbol(
			target.SourceLocation,
			target.Docs,
			SymbolTarget,
			tokens,
		); ok {
			doc.Symbols = append(doc.Symbols, symbol)
		}
	}

	for _, function := range tree.Functions {
		if symbol, ok := canonicalSymbol(
			function.SourceLocation,
			function.Docs,
			SymbolFunction,
			tokens,
		); ok {
			doc.Symbols = append(doc.Symbols, symbol)
		}
	}

	sort.Slice(doc.Symbols, func(i, j int) bool {
		return doc.Symbols[i].Selection.Start < doc.Symbols[j].Selection.Start
	})

	for _, item := range canonicalCommands(tree) {
		args := commandArguments(item.command.SourceLocation, tokens)
		if item.command.Name == earthfile.CmdImport {
			if imp, ok := canonicalImport(item.command, args, item.scope); ok {
				doc.Imports = append(doc.Imports, imp)
			}
		}

		doc.References = append(
			doc.References,
			canonicalReferences(item.command, args, text, item.scope)...,
		)
	}

	return doc
}

func canonicalSymbol(
	location *earthfile.SourceLocation,
	docs string,
	kind SymbolKind,
	tokens []earthfile.SourceToken,
) (Symbol, bool) {
	token, ok := declarationToken(location, tokens)
	if !ok {
		return Symbol{}, false
	}

	symbol, ok := parseDeclaration(token.Text, token.Start, nil)
	if !ok {
		return Symbol{}, false
	}

	symbol.Kind = kind
	symbol.Scope = symbol.Name
	symbol.Docs = strings.TrimSpace(docs)

	return symbol, true
}

func declarationToken(
	location *earthfile.SourceLocation,
	tokens []earthfile.SourceToken,
) (earthfile.SourceToken, bool) {
	if location == nil {
		return earthfile.SourceToken{}, false
	}

	for _, token := range tokens {
		if token.Line != location.StartLine || token.Column != location.StartColumn {
			continue
		}

		if token.Kind == earthfile.SourceTokenTarget || token.Kind == earthfile.SourceTokenFunction {
			return token, true
		}
	}

	return earthfile.SourceToken{}, false
}

func canonicalCommands(tree earthfile.Tree) []scopedCommand {
	var commands []scopedCommand
	appendBlockCommands(&commands, tree.BaseRecipe, "")

	for _, target := range tree.Targets {
		appendBlockCommands(&commands, target.Recipe, target.Name)
	}

	for _, function := range tree.Functions {
		scope := function.Name
		if tokenName := functionName(function.Name); tokenName != "" {
			scope = tokenName
		}

		appendBlockCommands(&commands, function.Recipe, scope)
	}

	sort.Slice(commands, func(i, j int) bool {
		left := commands[i].command.SourceLocation
		right := commands[j].command.SourceLocation

		if left == nil {
			return false
		}

		if right == nil {
			return true
		}

		if left.StartLine == right.StartLine {
			return left.StartColumn < right.StartColumn
		}

		return left.StartLine < right.StartLine
	})

	return commands
}

func appendBlockCommands(commands *[]scopedCommand, block earthfile.Block, scope string) {
	for _, statement := range block {
		switch {
		case statement.Command != nil:
			*commands = append(*commands, scopedCommand{command: *statement.Command, scope: scope})
		case statement.With != nil:
			*commands = append(*commands, scopedCommand{command: statement.With.Command, scope: scope})
			appendBlockCommands(commands, statement.With.Body, scope)
		case statement.If != nil:
			appendBlockCommands(commands, statement.If.IfBody, scope)

			for _, elseIf := range statement.If.ElseIf {
				appendBlockCommands(commands, elseIf.Body, scope)
			}

			if statement.If.ElseBody != nil {
				appendBlockCommands(commands, *statement.If.ElseBody, scope)
			}
		case statement.Try != nil:
			appendBlockCommands(commands, statement.Try.TryBody, scope)

			if statement.Try.CatchBody != nil {
				appendBlockCommands(commands, *statement.Try.CatchBody, scope)
			}

			if statement.Try.FinallyBody != nil {
				appendBlockCommands(commands, *statement.Try.FinallyBody, scope)
			}
		case statement.For != nil:
			appendBlockCommands(commands, statement.For.Body, scope)
		case statement.Wait != nil:
			appendBlockCommands(commands, statement.Wait.Body, scope)
		}
	}
}

func commandArguments(
	location *earthfile.SourceLocation,
	tokens []earthfile.SourceToken,
) []earthfile.SourceToken {
	if location == nil {
		return nil
	}

	var args []earthfile.SourceToken

	for _, token := range tokens {
		if token.Kind != earthfile.SourceTokenArgument ||
			!sourceLocationContains(location, token.Line, token.Column) {
			continue
		}

		args = append(args, token)
	}

	return args
}

func sourceLocationContains(location *earthfile.SourceLocation, line, column int) bool {
	if line < location.StartLine || line > location.EndLine {
		return false
	}

	if line == location.StartLine && column <= location.StartColumn {
		return false
	}

	if line == location.EndLine && location.EndColumn > 0 && column >= location.EndColumn {
		return false
	}

	return true
}

func canonicalImport(
	command earthfile.Command,
	args []earthfile.SourceToken,
	scope string,
) (Import, bool) {
	if len(command.Args) == 0 || len(args) == 0 {
		return Import{}, false
	}

	path := unquote(command.Args[0])

	alias := ""

	if len(command.Args) >= 3 && strings.EqualFold(command.Args[1], "AS") {
		alias = unquote(command.Args[2])
	}

	if alias == "" {
		alias = filepath.Base(filepath.Clean(path))
	}

	end := args[len(args)-1].End

	return Import{
		Path:      path,
		Alias:     alias,
		Scope:     scope,
		Range:     Range{Start: args[0].Start, End: end},
		PathRange: Range{Start: args[0].Start, End: args[0].End},
	}, true
}

func canonicalReferences(
	command earthfile.Command,
	args []earthfile.SourceToken,
	text string,
	scope string,
) []Reference {
	//nolint:exhaustive // Only commands whose operands can denote dependencies are relevant.
	switch command.Name {
	case earthfile.CmdBuild, earthfile.CmdFrom:
		return directReferences(args, scope, SymbolTarget, domain.ParseTarget)
	case earthfile.CmdDo:
		return directReferences(args, scope, SymbolFunction, domain.ParseCommand)
	case earthfile.CmdCopy:
		return copyReferences(args, text, scope)
	default:
		return nil
	}
}

func directReferences[T domain.Reference](
	args []earthfile.SourceToken,
	scope string,
	kind SymbolKind,
	parse func(string) (T, error),
) []Reference {
	var references []Reference

	for _, token := range args {
		value := unquote(token.Text)

		parsed, err := parse(value)
		if err != nil {
			continue
		}

		if ref, ok := canonicalReference(parsed, token.Text, token.Start, scope, kind); ok {
			references = append(references, ref)
		}
	}

	return references
}

func copyReferences(args []earthfile.SourceToken, text, scope string) []Reference {
	var references []Reference

	for index := 0; index < len(args); index++ {
		token := args[index]
		value := token.Text
		endIndex := index

		if strings.HasPrefix(value, "(") || strings.HasPrefix(value, "\"(") {
			for endIndex < len(args) {
				value = text[token.Start:args[endIndex].End]
				if flagutil.IsInParamsForm(value) {
					break
				}

				endIndex++
			}
		}

		artifactText := unquote(value)
		if flagutil.IsInParamsForm(value) {
			var err error

			artifactText, _, err = flagutil.ParseParams(value)
			if err != nil {
				index = endIndex
				continue
			}

			index = endIndex
		}

		artifact, err := domain.ParseArtifact(artifactText)
		if err != nil {
			continue
		}

		if ref, ok := canonicalReference(
			artifact.Target,
			value,
			token.Start,
			scope,
			SymbolTarget,
		); ok {
			references = append(references, ref)
		}
	}

	return references
}

func canonicalReference(
	parsed domain.Reference,
	source string,
	start int,
	scope string,
	kind SymbolKind,
) (Reference, bool) {
	raw := parsed.String()

	relativeStart := strings.Index(source, raw)
	if relativeStart < 0 {
		return Reference{}, false
	}

	return Reference{
		Raw:     raw,
		Project: parsed.ProjectCanonical(),
		Name:    parsed.GetName(),
		Scope:   scope,
		Kind:    kind,
		Range: Range{
			Start: start + relativeStart,
			End:   start + relativeStart + len(raw),
		},
	}, true
}

func functionName(value string) string {
	value = strings.TrimSuffix(value, ":")

	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}

	return fields[len(fields)-1]
}
