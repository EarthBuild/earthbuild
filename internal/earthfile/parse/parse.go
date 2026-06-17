package parse

//nolint:wsl

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// parser is the state representation of the Earthfile parser.
type parser struct {
	lex       *lexer
	token     [3]Item // 3-token lookahead for parser
	peekCount int
}

// next returns the next token.
func (p *parser) next() Item {
	if p.peekCount > 0 {
		p.peekCount--
	} else {
		p.token[0] = p.lex.nextItem()
	}

	return p.token[p.peekCount]
}

// peek returns but does not consume the next token.
func (p *parser) peek() Item {
	if p.peekCount > 0 {
		return p.token[p.peekCount-1]
	}

	p.peekCount = 1
	p.token[0] = p.lex.nextItem()

	return p.token[0]
}

// errorf formats an error and terminates processing.
func (p *parser) errorf(pos Pos, format string, args ...any) error {
	p.lex.drain()
	return fmt.Errorf("syntax error at pos %d: %s", pos, fmt.Sprintf(format, args...))
}

// Parse parses the Earthfile text and returns a Tree.
func Parse(name, text string) (Tree, error) {
	p := &parser{
		lex: lex(name, text),
	}

	return p.parseEarthfile()
}

// parseEarthfile is the top-level entry point for recursive descent.
func (p *parser) parseEarthfile() (Tree, error) {
	var (
		ef                Tree
		pendingDocsTokens []string
	)

	sawNL := false

	for {
		token := p.peek()
		switch token.Typ {
		case ItemEOF:
			p.next() // consume EOF

			var targets []Target

			for _, t := range ef.Targets {
				isFunction := false

				if len(t.Recipe) > 0 && t.Recipe[0].Command != nil {
					if t.Recipe[0].Command.Name == CmdCommand || t.Recipe[0].Command.Name == CmdFunction {
						isFunction = true
					}
				}

				if isFunction {
					fn := Function{
						SourceLocation: t.SourceLocation,
						Name:           t.Name,
						Recipe:         t.Recipe,
					}
					ef.Functions = append(ef.Functions, fn)
				} else {
					targets = append(targets, t)
				}
			}

			ef.Targets = targets

			return ef, nil
		case ItemNL, ItemWS, ItemEOLComment:
			tok := p.next()
			if tok.Typ == ItemNL {
				if sawNL {
					pendingDocsTokens = nil
					sawNL = false
				} else {
					sawNL = true
				}
			}
		case ItemComment:
			tok := p.next()
			pendingDocsTokens = append(pendingDocsTokens, tok.Val)
			sawNL = false
		case ItemVersion:
			sawNL = false
			pendingDocsTokens = nil

			if token.Col > 1 {
				return ef, p.errorf(token.Pos, "VERSION command must start at the beginning of the line")
			}

			version, err := p.parseVersion()
			if err != nil {
				return ef, err
			}

			ef.Version = &version
		case ItemTarget:
			sawNL = false

			if token.Col > 1 {
				return ef, p.errorf(token.Pos, "target must start at the beginning of the line")
			}

			target, err := p.parseTarget()
			if err == nil && len(pendingDocsTokens) > 0 {
				target.Docs = computeDocs(pendingDocsTokens)
				pendingDocsTokens = nil
			}

			if err != nil {
				return ef, err
			}

			ef.Targets = append(ef.Targets, target)
		case ItemFunction, ItemUserCommand:
			sawNL = false

			if token.Col > 1 {
				return ef, p.errorf(token.Pos, "function/command must start at the beginning of the line")
			}

			fn, err := p.parseFunction()
			if err != nil {
				return ef, err
			}

			ef.Functions = append(ef.Functions, fn)
		case ItemIf:
			sawNL = false

			stmt, err := p.parseIf()
			if err != nil {
				return ef, err
			}

			ef.BaseRecipe = append(ef.BaseRecipe, Statement{If: &stmt})
		case ItemWith:
			sawNL = false

			stmt, err := p.parseWith()
			if err != nil {
				return ef, err
			}

			ef.BaseRecipe = append(ef.BaseRecipe, Statement{With: &stmt})
		case ItemFor:
			sawNL = false

			stmt, err := p.parseFor()
			if err != nil {
				return ef, err
			}

			ef.BaseRecipe = append(ef.BaseRecipe, Statement{For: &stmt})
		case ItemTry:
			stmt, err := p.parseTry()
			if err != nil {
				return ef, err
			}

			ef.BaseRecipe = append(ef.BaseRecipe, Statement{Try: &stmt})
		case ItemWait:
			stmt, err := p.parseWait()
			if err != nil {
				return ef, err
			}

			ef.BaseRecipe = append(ef.BaseRecipe, Statement{Wait: &stmt})
		default:
			if isCommandToken(token.Typ) {
				if token.Col > 1 {
					return ef, p.errorf(token.Pos, "command at top level must start at the beginning of the line")
				}

				cmd, err := p.parseCommand()
				if err != nil {
					return ef, err
				}

				if len(pendingDocsTokens) > 0 {
					cmd.Docs = computeDocs(pendingDocsTokens)
					pendingDocsTokens = nil
				}

				ef.BaseRecipe = append(ef.BaseRecipe, Statement{Command: &cmd})
			} else {
				return ef, p.errorf(
					token.Pos,
					"unexpected token at top level: type %d (%s) at line %d",
					token.Typ, token.Val, token.Line,
				)
			}
		}
	}
}

func isCommandToken(t ItemType) bool {
	switch t {
	case ItemFrom, ItemFromDockerfile, ItemLocally, ItemCopy, ItemSaveArtifact,
		ItemSaveImage, ItemRun, ItemExpose, ItemVolume, ItemEnv, ItemArg,
		ItemSet, ItemLet, ItemLabel, ItemBuild, ItemWorkdir, ItemUser,
		ItemCmd, ItemEntrypoint, ItemGitClone, ItemAdd, ItemStopSignal,
		ItemOnBuild, ItemHealthCheck, ItemShell, ItemDo, ItemCommand,
		ItemFunctionKW, ItemImport, ItemCache, ItemHost, ItemProject,
		ItemWith, ItemIf, ItemFor, ItemWait, ItemTry:
		return true
	}

	return false
}

// parseVersion parses a VERSION command and its arguments.
func (p *parser) parseVersion() (Version, error) {
	var v Version

	token := p.next() // consume ItemVersion
	v.SourceLocation = &SourceLocation{
		StartLine:   token.Line,
		StartColumn: token.Col,
	}

	for {
		tok := p.peek()
		switch tok.Typ {
		case ItemAtom:
			p.next()

			v.Args = append(v.Args, tok.Val)
		case ItemWS:
			p.next()
			// ignore whitespace between args
		case ItemNL, ItemComment, ItemEOLComment:
			p.next()

			v.SourceLocation.EndLine = tok.Line
			v.SourceLocation.EndColumn = tok.Col

			return v, nil
		case ItemEOF:
			v.SourceLocation.EndLine = tok.Line
			v.SourceLocation.EndColumn = tok.Col

			return v, nil
		default:
			p.next()
			return v, p.errorf(tok.Pos, "unexpected token in VERSION command: %s", tok.Val)
		}
	}
}

// parseTarget parses a target and its recipe block.
func (p *parser) parseTarget() (Target, error) {
	var target Target

	tok := p.next() // consume ItemTarget
	target.Name = strings.TrimSuffix(tok.Val, ":")
	target.SourceLocation = &SourceLocation{
		StartLine:   tok.Line,
		StartColumn: tok.Col,
	}

	// Read block
	block, err := p.parseBlock()
	if err != nil {
		return target, err
	}

	target.Recipe = block

	// The EndLine is the last item in the block or the target declaration itself
	target.SourceLocation.EndLine = p.peek().Line
	target.SourceLocation.EndColumn = p.peek().Col

	return target, nil
}

func (p *parser) parseFunction() (Function, error) {
	fn := Function{
		Name: p.peek().Val,
		SourceLocation: &SourceLocation{
			StartLine:   p.peek().Line,
			StartColumn: p.peek().Col,
		},
	}
	p.next() // consume
	block, err := p.parseBlock()
	fn.Recipe = block

	return fn, err
}

func (p *parser) parseStmts() (Block, error) {
	var block Block

	var pendingDocsTokens []string

	for {
		tok := p.peek()
		switch tok.Typ {
		case ItemDedent, ItemEOF, ItemEnd, ItemElseIf, ItemElse, ItemCatch, ItemFinally:
			return block, nil
		case ItemNL, ItemWS, ItemEOLComment, ItemIndent:
			p.next()
			continue
		case ItemComment:
			tok = p.next()
			pendingDocsTokens = append(pendingDocsTokens, tok.Val)

			continue
		case ItemIf:
			pendingDocsTokens = nil

			ifStmt, err := p.parseIf()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{If: &ifStmt})
		case ItemFor:
			pendingDocsTokens = nil

			forStmt, err := p.parseFor()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{For: &forStmt})
		case ItemWait:
			pendingDocsTokens = nil

			waitStmt, err := p.parseWait()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{Wait: &waitStmt})
		case ItemTry:
			pendingDocsTokens = nil

			tryStmt, err := p.parseTry()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{Try: &tryStmt})
		case ItemWith:
			pendingDocsTokens = nil

			withStmt, err := p.parseWith()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{With: &withStmt})
		case ItemFrom, ItemFromDockerfile, ItemLocally, ItemCopy, ItemSaveArtifact,
			ItemSaveImage, ItemRun, ItemExpose, ItemVolume, ItemEnv, ItemArg,
			ItemSet, ItemLet, ItemLabel, ItemBuild, ItemWorkdir, ItemUser,
			ItemCmd, ItemEntrypoint, ItemGitClone, ItemAdd, ItemStopSignal,
			ItemOnBuild, ItemHealthCheck, ItemShell, ItemDo, ItemCommand,
			ItemFunctionKW, ItemImport, ItemVersion, ItemCache, ItemHost,
			ItemProject:
			cmd, err := p.parseCommand()
			if err != nil {
				return block, err
			}

			if len(pendingDocsTokens) > 0 {
				docs := strings.Join(pendingDocsTokens, "\n")
				cmd.Docs = docs
				pendingDocsTokens = nil
			}

			block = append(block, Statement{Command: &cmd})
		case ItemTarget, ItemUserCommand:
			return block, p.errorf(tok.Pos, "unexpected token in recipe block: type %d (%s)", tok.Typ, tok.Val)
		default:
			return block, p.errorf(tok.Pos, "unexpected token in recipe block: type %d (%s)", tok.Typ, tok.Val)
		}
	}
}

func (p *parser) parseBlock() (Block, error) {
	var (
		block             Block
		pendingDocsTokens []string
	)

	sawNL := false
	// Expect optional NLs/Comments and then an Indent
	for {
		t := p.peek()
		if t.Typ == ItemNL || t.Typ == ItemWS || t.Typ == ItemEOLComment {
			p.next()
			continue
		}

		if t.Typ == ItemComment {
			t = p.next()
			pendingDocsTokens = append(pendingDocsTokens, t.Val)

			continue
		}

		if t.Typ == ItemIndent {
			p.next() // consume indent
			break
		}

		if t.Typ == ItemEOF || t.Typ == ItemTarget || t.Typ == ItemUserCommand || t.Typ == ItemFunction {
			// empty block
			return block, nil
		}

		return block, p.errorf(t.Pos, "expected block indentation, got %s", t.Val)
	}

	for {
		tok := p.peek()

		switch tok.Typ {
		case ItemDedent, ItemEOF:
			if tok.Typ == ItemDedent {
				p.next()
			}

			return block, nil
		case ItemNL, ItemWS, ItemEOLComment:
			tok = p.next()
			if tok.Typ == ItemNL {
				if sawNL {
					pendingDocsTokens = nil
					sawNL = false
				} else {
					sawNL = true
				}
			}

			continue
		case ItemComment:
			tok = p.next()
			pendingDocsTokens = append(pendingDocsTokens, tok.Val)
			sawNL = false

			continue
		case ItemIf:
			sawNL = false
			pendingDocsTokens = nil

			ifStmt, err := p.parseIf()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{If: &ifStmt})
		case ItemFor:
			sawNL = false
			pendingDocsTokens = nil

			forStmt, err := p.parseFor()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{For: &forStmt})
		case ItemTry:
			sawNL = false
			pendingDocsTokens = nil

			tryStmt, err := p.parseTry()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{Try: &tryStmt})
		case ItemWith:
			sawNL = false

			withStmt, err := p.parseWith()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{With: &withStmt})
		case ItemWait:
			sawNL = false
			pendingDocsTokens = nil

			waitStmt, err := p.parseWait()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{Wait: &waitStmt})
		case ItemFrom, ItemFromDockerfile, ItemLocally, ItemCopy, ItemSaveArtifact,
			ItemSaveImage, ItemRun, ItemExpose, ItemVolume, ItemEnv, ItemArg,
			ItemSet, ItemLet, ItemLabel, ItemBuild, ItemWorkdir, ItemUser,
			ItemCmd, ItemEntrypoint, ItemGitClone, ItemAdd, ItemStopSignal,
			ItemOnBuild, ItemHealthCheck, ItemShell, ItemDo, ItemCommand,
			ItemFunctionKW, ItemImport, ItemVersion, ItemCache, ItemHost,
			ItemProject:
			sawNL = false

			cmd, err := p.parseCommand()
			if err != nil {
				return block, err
			}

			if len(pendingDocsTokens) > 0 {
				cmd.Docs = computeDocs(pendingDocsTokens)
				pendingDocsTokens = nil
			}

			block = append(block, Statement{Command: &cmd})
		default:
			return block, p.errorf(tok.Pos, "unexpected token in recipe block: type %d (%s)", tok.Typ, tok.Val)
		}
	}
}

func (p *parser) parseCommand() (Command, error) {
	var cmd Command

	tok := p.next()
	cmd.Name = tok.Val
	cmd.SourceLocation = &SourceLocation{
		StartLine:   tok.Line,
		StartColumn: tok.Col,
	}

	var args []string

	var endLoc SourceLocation

	var err error

	switch cmd.Name {
	case CmdEnv, CmdArg, CmdSet, CmdLet:
		args, endLoc, err = p.parseKeyValueCommandArgs()
	default:
		args, endLoc, err = p.parseArgsUntilNL()
	}

	if err != nil {
		return cmd, err
	}

	for i := range args {
		args[i] = replaceEscape(args[i])
	}

	cmd.Args = args

	if len(cmd.Args) > 0 {
		switch cmd.Name {
		case CmdRun, CmdCmd, CmdEntrypoint, CmdVolume:
			joined := strings.Join(cmd.Args, "")

			var jsonArgs []string

			err := json.Unmarshal([]byte(joined), &jsonArgs)
			if err == nil {
				cmd.Args = jsonArgs
				cmd.ExecMode = true
			}
		case CmdLabel:
			var newArgs []string
			for _, arg := range cmd.Args {
				newArgs = append(newArgs, splitKeyValueArg(arg)...)
			}

			cmd.Args = newArgs
		}
	}

	cmd.SourceLocation.EndLine = endLoc.EndLine
	cmd.SourceLocation.EndColumn = endLoc.EndColumn

	return cmd, nil
}

func (p *parser) parseArgsUntilNL() ([]string, SourceLocation, error) {
	args := []string{}

	var endLoc SourceLocation

	for {
		t := p.peek()
		switch t.Typ {
		case ItemAtom:
			p.next()

			args = append(args, t.Val)
		case ItemWS, ItemComment:
			p.next()
			// ignore
		case ItemNL, ItemEOLComment:
			p.next()

			endLoc.EndLine = t.Line
			endLoc.EndColumn = t.Col

			return args, endLoc, nil
		case ItemEOF, ItemDedent:
			// DO NOT consume EOF or Dedent!
			endLoc.EndLine = t.Line
			endLoc.EndColumn = t.Col

			return args, endLoc, nil
		default:
			p.next()
			return nil, endLoc, p.errorf(t.Pos, "unexpected token in args: %s", t.Val)
		}
	}
}

func (p *parser) parseKeyValueCommandArgs() ([]string, SourceLocation, error) {
	var args []string

	var endLoc SourceLocation

	var items []Item

	for {
		t := p.peek()
		if t.Typ == ItemNL || t.Typ == ItemEOLComment {
			p.next() // consume NL or EOL comment

			endLoc.EndLine = t.Line
			endLoc.EndColumn = t.Col

			break
		}

		if t.Typ == ItemEOF || t.Typ == ItemDedent {
			// DO NOT consume EOF or Dedent!
			endLoc.EndLine = t.Line
			endLoc.EndColumn = t.Col

			break
		}

		if t.Typ == ItemError {
			p.next()

			return nil, endLoc, p.errorf(t.Pos, "unexpected token: %s", t.Val)
		}

		p.next()

		items = append(items, t)
	}

	args = parseKeyValueItems(items)

	return args, endLoc, nil
}

func parseKeyValueItems(items []Item) []string {
	var args []string

	idx := 0
	// Skip leading WS
	for idx < len(items) && items[idx].Typ == ItemWS {
		idx++
	}

	// Read flags
	for idx < len(items) {
		if items[idx].Typ == ItemAtom && strings.HasPrefix(items[idx].Val, "-") {
			args = append(args, items[idx].Val)
			idx++
			// skip WS after flag
			for idx < len(items) && items[idx].Typ == ItemWS {
				idx++
			}
		} else {
			break
		}
	}

	// Now we expect the key. If key is missing, fallback:
	if idx >= len(items) || items[idx].Typ != ItemAtom {
		for _, item := range items {
			if item.Typ == ItemAtom {
				args = append(args, item.Val)
			}
		}

		return args
	}

	keyToken := items[idx].Val
	idx++

	var (
		hasEquals bool
		key       string
		valStart  string
	)

	keyParts := splitKeyValueArg(keyToken)
	if len(keyParts) > 1 {
		key = keyParts[0]
		hasEquals = true

		if len(keyParts) > 2 {
			valStart = keyParts[2]
		}
	} else {
		key = keyToken
	}

	// skip WS after key only if we haven't found the equals sign in the key token
	if !hasEquals {
		for idx < len(items) && items[idx].Typ == ItemWS {
			idx++
		}
	}

	args = append(args, key)

	// If there wasn't an equals sign in the key token itself, check the next token
	if !hasEquals {
		if idx < len(items) && items[idx].Typ == ItemAtom && items[idx].Val == "=" {
			args = append(args, "=")
			hasEquals = true
			idx++
			// skip WS after '='
			for idx < len(items) && items[idx].Typ == ItemWS {
				idx++
			}
		}
	} else {
		args = append(args, "=")
	}

	// Everything else is the value!
	var valBuilder strings.Builder
	if valStart != "" {
		valBuilder.WriteString(valStart)
	}

	for i := idx; i < len(items); i++ {
		valBuilder.WriteString(items[i].Val)
	}

	val := valBuilder.String()

	val = strings.TrimRight(val, " \t\r\n")

	if val != "" {
		if !hasEquals {
			args = append(args, "=")
		}

		args = append(args, val)
	} else if hasEquals {
		args = append(args, "")
	}

	return args
}

func (p *parser) parseIf() (IfStatement, error) {
	ifStmt := IfStatement{}

	t := p.next()
	ifStmt.SourceLocation = &SourceLocation{StartLine: t.Line, StartColumn: t.Col}

	args, endLoc, err := p.parseArgsUntilNL()
	if err != nil {
		return ifStmt, err
	}

	ifStmt.Expression = args
	ifStmt.SourceLocation.EndLine = endLoc.EndLine
	ifStmt.SourceLocation.EndColumn = endLoc.EndColumn

	block, err := p.parseStmts()
	if err != nil {
		return ifStmt, err
	}

	ifStmt.IfBody = block

	for {
		tok := p.peek()
		switch tok.Typ {
		case ItemDedent:
			p.next() // consume dedent
		case ItemElseIf:
			p.next() // consume ItemElseIf

			args, endLoc, err := p.parseArgsUntilNL()
			if err != nil {
				return ifStmt, err
			}

			eiBlock, err := p.parseStmts()
			if err != nil {
				return ifStmt, err
			}

			ifStmt.ElseIf = append(ifStmt.ElseIf, ElseIf{
				SourceLocation: &SourceLocation{
					StartLine:   tok.Line,
					StartColumn: tok.Col,
					EndLine:     endLoc.EndLine,
					EndColumn:   endLoc.EndColumn,
				},
				Expression: args,
				Body:       eiBlock,
			})
		case ItemElse:
			p.next() // consume ItemElse

			_, _, err := p.parseArgsUntilNL() // parse till NL
			if err != nil {
				return ifStmt, err
			}

			eBlock, err := p.parseStmts()
			if err != nil {
				return ifStmt, err
			}

			ifStmt.ElseBody = &eBlock
		case ItemEnd:
			p.next() // consume END

			_, _, err := p.parseArgsUntilNL()
			if err != nil {
				return ifStmt, err
			}

			return ifStmt, nil
		default:
			return ifStmt, p.errorf(tok.Pos, "expected END to close IF statement, got %s", tok.Val)
		}
	}
}

func (p *parser) parseFor() (ForStatement, error) {
	forStmt := ForStatement{}

	t := p.next()
	forStmt.SourceLocation = &SourceLocation{StartLine: t.Line, StartColumn: t.Col}

	args, _, err := p.parseArgsUntilNL()
	if err != nil {
		return forStmt, err
	}

	forStmt.Args = args

	block, err := p.parseStmts()
	if err != nil {
		return forStmt, err
	}

	forStmt.Body = block

	if p.peek().Typ == ItemDedent {
		p.next()
	}

	tok := p.peek()
	if tok.Typ != ItemEnd {
		return forStmt, p.errorf(tok.Pos, "expected END to close FOR statement, got %s", tok.Val)
	}

	p.next()

	_, _, err = p.parseArgsUntilNL()
	if err != nil {
		return forStmt, err
	}

	return forStmt, nil
}

func (p *parser) parseTry() (TryStatement, error) {
	tryStmt := TryStatement{}

	t := p.next()
	tryStmt.SourceLocation = &SourceLocation{StartLine: t.Line, StartColumn: t.Col}

	_, _, err := p.parseArgsUntilNL()
	if err != nil {
		return tryStmt, err
	}

	block, err := p.parseStmts()
	if err != nil {
		return tryStmt, err
	}

	tryStmt.TryBody = block

	for {
		tok := p.peek()
		switch tok.Typ {
		case ItemDedent:
			p.next()
		case ItemCatch:
			p.next()

			_, _, err := p.parseArgsUntilNL()
			if err != nil {
				return tryStmt, err
			}

			catchBlock, err := p.parseStmts()
			if err != nil {
				return tryStmt, err
			}

			tryStmt.CatchBody = &catchBlock
		case ItemFinally:
			p.next()

			_, _, err := p.parseArgsUntilNL()
			if err != nil {
				return tryStmt, err
			}

			finallyBlock, err := p.parseStmts()
			if err != nil {
				return tryStmt, err
			}

			tryStmt.FinallyBody = &finallyBlock
		case ItemEnd:
			p.next()

			_, _, err := p.parseArgsUntilNL()
			if err != nil {
				return tryStmt, err
			}

			return tryStmt, nil
		default:
			return tryStmt, p.errorf(tok.Pos, "expected END to close TRY statement, got %s", tok.Val)
		}
	}
}

func (p *parser) parseWith() (WithStatement, error) {
	withStmt := WithStatement{}

	t := p.next()
	withStmt.SourceLocation = &SourceLocation{StartLine: t.Line, StartColumn: t.Col}

	for p.peek().Typ == ItemWS {
		p.next()
	}

	cmd, err := p.parseCommand()
	if err != nil {
		return withStmt, err
	}

	withStmt.Command = cmd

	block, err := p.parseStmts()
	if err != nil {
		return withStmt, err
	}

	withStmt.Body = block

	if p.peek().Typ == ItemDedent {
		p.next()
	}

	tok := p.peek()
	if tok.Typ != ItemEnd {
		return withStmt, p.errorf(tok.Pos, "expected END to close WITH statement, got %s", tok.Val)
	}

	p.next()

	_, _, err = p.parseArgsUntilNL()
	if err != nil {
		return withStmt, err
	}

	return withStmt, nil
}

func (p *parser) parseWait() (WaitStatement, error) {
	waitStmt := WaitStatement{}

	t := p.next()
	waitStmt.SourceLocation = &SourceLocation{StartLine: t.Line, StartColumn: t.Col}

	_, _, err := p.parseArgsUntilNL()
	if err != nil {
		return waitStmt, err
	}

	block, err := p.parseStmts()
	if err != nil {
		return waitStmt, err
	}

	waitStmt.Body = block

	if p.peek().Typ == ItemDedent {
		p.next()
	}

	tok := p.peek()
	if tok.Typ != ItemEnd {
		return waitStmt, p.errorf(tok.Pos, "expected END to close WAIT statement, got %s", tok.Val)
	}

	p.next()

	_, _, err = p.parseArgsUntilNL()
	if err != nil {
		return waitStmt, err
	}

	return waitStmt, nil
}

func computeDocs(comments []string) string {
	if len(comments) == 0 {
		return ""
	}

	var (
		docs        strings.Builder
		leadingTrim string
	)

	for i, c := range comments {
		line := strings.TrimSpace(c)
		line = strings.TrimPrefix(line, "#")

		if i == 0 {
			var trimRunes []rune

			for _, r := range line {
				if r == ' ' || r == '\t' {
					trimRunes = append(trimRunes, r)
				} else {
					break
				}
			}

			leadingTrim = string(trimRunes)
		}

		line = strings.TrimPrefix(line, leadingTrim)
		docs.WriteString(line)
		docs.WriteByte('\n')
	}

	return docs.String()
}

func splitKeyValueArg(s string) []string {
	var out []string

	inSingle := false
	inDouble := false
	start := 0

	for i := range len(s) {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '=' && !inSingle && !inDouble:
			if i > start {
				out = append(out, s[start:i])
			}

			out = append(out, "=")
			start = i + 1

			if start < len(s) {
				out = append(out, s[start:])
			}

			return out
		}
	}

	if start < len(s) {
		out = append(out, s[start:])
	}

	return out
}

var stripLineContinuationRegexp = regexp.MustCompile(
	`^\\[ \t]*(#[^\n\r]*)?(\n|(\r\n))[\t ]*((#[^\n\r]*)?(\n|(\r\n))[\t ]*)*`,
)

func replaceEscape(str string) string {
	var (
		sb       strings.Builder
		inDouble bool
		inSingle bool
		i        int
	)

	for i < len(str) {
		c := str[i]

		switch {
		case c == '\\':
			switch {
			case inDouble:
				sb.WriteByte(c)

				i++

				if i < len(str) {
					sb.WriteByte(str[i])

					i++
				}

			case inSingle:
				sb.WriteByte(c)

				i++

				if i < len(str) {
					sb.WriteByte(str[i])

					i++
				}

			default:
				rem := str[i:]
				loc := stripLineContinuationRegexp.FindStringIndex(rem)

				if loc != nil && loc[0] == 0 {
					i += loc[1]
				} else {
					sb.WriteByte(c)

					i++

					if i < len(str) {
						sb.WriteByte(str[i])

						i++
					}
				}
			}

		case c == '"' && !inSingle:
			inDouble = !inDouble

			sb.WriteByte(c)

			i++

		case c == '\'' && !inDouble:
			inSingle = !inSingle

			sb.WriteByte(c)

			i++

		default:
			sb.WriteByte(c)

			i++
		}
	}

	return sb.String()
}
