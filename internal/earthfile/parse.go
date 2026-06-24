package earthfile

//nolint:wsl
import (
	"fmt"
	"io"
	"os"
	"strings"
)

type parseConfig struct {
	enableSourceMap bool
}

// ParseOption is an option function for customizing the behavior of the parser.
type ParseOption func(*parseConfig)

// WithSourceMap tells the parser to enable a source map when parsing.
func WithSourceMap() ParseOption {
	return func(c *parseConfig) {
		c.enableSourceMap = true
	}
}

// ParseFile parses the Earthfile at the given path into an AST.
func ParseFile(path string, opts ...ParseOption) (Tree, error) {
	f, err := os.Open(path) // #nosec G304
	if err != nil {
		return Tree{}, fmt.Errorf("earthfile: unable to open file '%v': %w", path, err)
	}
	defer f.Close() // #nosec G104

	b, err := io.ReadAll(f)
	if err != nil {
		return Tree{}, fmt.Errorf("ast: could not read Earthfile for parsing: %w", err)
	}

	return Parse(path, string(b), opts...)
}

// Parse parses the Earthfile text into an AST.
func Parse(name, text string, opts ...ParseOption) (Tree, error) {
	var cfg parseConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	p := &parser{
		lex: lex(name, text),
	}

	ef, err := p.parseEarthfile()
	if err != nil {
		return Tree{}, err
	}

	// Set file path on SourceLocations if they exist and are requested
	if cfg.enableSourceMap {
		setSourceLocationFile(&ef, name)
	} else {
		removeSourceLocations(&ef)
	}

	err = validateAst(ef)
	if err != nil {
		return Tree{}, err
	}

	return ef, nil
}

func setSourceLocationFile(ef *Tree, filename string) {
	if ef.SourceLocation != nil {
		ef.SourceLocation.File = filename
	}

	if ef.Version != nil && ef.Version.SourceLocation != nil {
		ef.Version.SourceLocation.File = filename
	}

	for i := range ef.Targets {
		if ef.Targets[i].SourceLocation != nil {
			ef.Targets[i].SourceLocation.File = filename
		}

		setBlockSourceLocationFile(ef.Targets[i].Recipe, filename)
	}

	for i := range ef.Functions {
		if ef.Functions[i].SourceLocation != nil {
			ef.Functions[i].SourceLocation.File = filename
		}

		setBlockSourceLocationFile(ef.Functions[i].Recipe, filename)
	}

	setBlockSourceLocationFile(ef.BaseRecipe, filename)
}

func setBlockSourceLocationFile(block Block, filename string) {
	for i := range block {
		if block[i].SourceLocation != nil {
			block[i].SourceLocation.File = filename
		}

		if block[i].Command != nil && block[i].Command.SourceLocation != nil {
			block[i].Command.SourceLocation.File = filename
		}

		if block[i].If != nil {
			if block[i].If.SourceLocation != nil {
				block[i].If.SourceLocation.File = filename
			}

			setBlockSourceLocationFile(block[i].If.IfBody, filename)

			for j := range block[i].If.ElseIf {
				if block[i].If.ElseIf[j].SourceLocation != nil {
					block[i].If.ElseIf[j].SourceLocation.File = filename
				}

				setBlockSourceLocationFile(block[i].If.ElseIf[j].Body, filename)
			}

			if block[i].If.ElseBody != nil {
				setBlockSourceLocationFile(*block[i].If.ElseBody, filename)
			}
		}

		if block[i].For != nil {
			if block[i].For.SourceLocation != nil {
				block[i].For.SourceLocation.File = filename
			}

			setBlockSourceLocationFile(block[i].For.Body, filename)
		}

		if block[i].Try != nil {
			if block[i].Try.SourceLocation != nil {
				block[i].Try.SourceLocation.File = filename
			}

			setBlockSourceLocationFile(block[i].Try.TryBody, filename)

			if block[i].Try.CatchBody != nil {
				setBlockSourceLocationFile(*block[i].Try.CatchBody, filename)
			}

			if block[i].Try.FinallyBody != nil {
				setBlockSourceLocationFile(*block[i].Try.FinallyBody, filename)
			}
		}

		if block[i].With != nil {
			if block[i].With.SourceLocation != nil {
				block[i].With.SourceLocation.File = filename
			}

			if block[i].With.Command.SourceLocation != nil {
				block[i].With.Command.SourceLocation.File = filename
			}

			setBlockSourceLocationFile(block[i].With.Body, filename)
		}

		if block[i].Wait != nil {
			if block[i].Wait.SourceLocation != nil {
				block[i].Wait.SourceLocation.File = filename
			}

			setBlockSourceLocationFile(block[i].Wait.Body, filename)
		}
	}
}

func removeSourceLocations(ef *Tree) {
	ef.SourceLocation = nil
	if ef.Version != nil {
		ef.Version.SourceLocation = nil
	}

	for i := range ef.Targets {
		ef.Targets[i].SourceLocation = nil
		removeBlockSourceLocations(ef.Targets[i].Recipe)
	}

	for i := range ef.Functions {
		ef.Functions[i].SourceLocation = nil
		removeBlockSourceLocations(ef.Functions[i].Recipe)
	}

	removeBlockSourceLocations(ef.BaseRecipe)
}

func removeBlockSourceLocations(block Block) {
	for i := range block {
		block[i].SourceLocation = nil

		if block[i].Command != nil {
			block[i].Command.SourceLocation = nil
		}

		if block[i].If != nil {
			block[i].If.SourceLocation = nil
			removeBlockSourceLocations(block[i].If.IfBody)

			for j := range block[i].If.ElseIf {
				block[i].If.ElseIf[j].SourceLocation = nil
				removeBlockSourceLocations(block[i].If.ElseIf[j].Body)
			}

			if block[i].If.ElseBody != nil {
				removeBlockSourceLocations(*block[i].If.ElseBody)
			}
		}

		if block[i].For != nil {
			block[i].For.SourceLocation = nil
			removeBlockSourceLocations(block[i].For.Body)
		}

		if block[i].Try != nil {
			block[i].Try.SourceLocation = nil
			removeBlockSourceLocations(block[i].Try.TryBody)

			if block[i].Try.CatchBody != nil {
				removeBlockSourceLocations(*block[i].Try.CatchBody)
			}

			if block[i].Try.FinallyBody != nil {
				removeBlockSourceLocations(*block[i].Try.FinallyBody)
			}
		}

		if block[i].With != nil {
			block[i].With.SourceLocation = nil
			block[i].With.Command.SourceLocation = nil
			removeBlockSourceLocations(block[i].With.Body)
		}

		if block[i].Wait != nil {
			block[i].Wait.SourceLocation = nil
			removeBlockSourceLocations(block[i].Wait.Body)
		}
	}
}

// parser is the state representation of the Earthfile parser.
type parser struct {
	lex       *lexer
	itemsBuf  []item
	token     [3]item // 3-token lookahead for parser
	peekCount int
}

// next returns the next token.
func (p *parser) next() item {
	if p.peekCount > 0 {
		p.peekCount--
	} else {
		p.token[0] = p.lex.nextItem()
	}

	return p.token[p.peekCount]
}

// peek returns but does not consume the next token.
func (p *parser) peek() item {
	if p.peekCount > 0 {
		return p.token[p.peekCount-1]
	}

	p.peekCount = 1
	p.token[0] = p.lex.nextItem()

	return p.token[0]
}

// errorf formats an error and terminates processing.
func (p *parser) errorf(pos pos, format string, args ...any) error {
	return fmt.Errorf("syntax error at pos %d: %s", pos, fmt.Sprintf(format, args...))
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
		// Only structural block keywords and top-level commands are handled explicitly
		// in the top-level parse loop. Commands and arguments are delegated to sub-parsers.
		//nolint:exhaustive
		switch token.Typ {
		case itemEOF:
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
		case itemError:
			p.next()
			return ef, p.errorf(token.pos, "%s", token.Val)
		case itemNL, itemWS, itemEOLComment:
			tok := p.next()
			if tok.Typ == itemNL {
				if sawNL {
					pendingDocsTokens = nil
					sawNL = false
				} else {
					sawNL = true
				}
			}
		case itemComment:
			tok := p.next()
			pendingDocsTokens = append(pendingDocsTokens, tok.Val)
			sawNL = false
		case itemVersion:
			sawNL = false
			pendingDocsTokens = nil

			if token.Col > 1 {
				return ef, p.errorf(token.pos, "VERSION command must start at the beginning of the line")
			}

			version, err := p.parseVersion()
			if err != nil {
				return ef, err
			}

			ef.Version = &version
		case itemTarget:
			sawNL = false

			if token.Col > 1 {
				return ef, p.errorf(token.pos, "target must start at the beginning of the line")
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
		case itemFunction, itemUserCommand:
			sawNL = false

			if token.Col > 1 {
				return ef, p.errorf(token.pos, "function/command must start at the beginning of the line")
			}

			fn, err := p.parseFunction()
			if err != nil {
				return ef, err
			}

			ef.Functions = append(ef.Functions, fn)
		case itemIf:
			sawNL = false

			stmt, err := p.parseIf()
			if err != nil {
				return ef, err
			}

			ef.BaseRecipe = append(ef.BaseRecipe, Statement{If: &stmt})
		case itemWith:
			sawNL = false

			stmt, err := p.parseWith()
			if err != nil {
				return ef, err
			}

			ef.BaseRecipe = append(ef.BaseRecipe, Statement{With: &stmt})
		case itemFor:
			sawNL = false

			stmt, err := p.parseFor()
			if err != nil {
				return ef, err
			}

			ef.BaseRecipe = append(ef.BaseRecipe, Statement{For: &stmt})
		case itemTry:
			stmt, err := p.parseTry()
			if err != nil {
				return ef, err
			}

			ef.BaseRecipe = append(ef.BaseRecipe, Statement{Try: &stmt})
		case itemWait:
			stmt, err := p.parseWait()
			if err != nil {
				return ef, err
			}

			ef.BaseRecipe = append(ef.BaseRecipe, Statement{Wait: &stmt})
		default:
			if isCommandToken(token.Typ) {
				if token.Col > 1 {
					return ef, p.errorf(token.pos, "command at top level must start at the beginning of the line")
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
					token.pos,
					"unexpected token at top level: type %d (%s) at line %d",
					token.Typ, token.Val, token.Line,
				)
			}
		}
	}
}

func isCommandToken(t itemType) bool {
	switch t {
	case itemFrom, itemFromDockerfile, itemLocally, itemCopy, itemSaveArtifact,
		itemSaveImage, itemRun, itemExpose, itemVolume, itemEnv, itemArg,
		itemSet, itemLet, itemLabel, itemBuild, itemWorkdir, itemUser,
		itemCmd, itemEntrypoint, itemGitClone, itemAdd, itemStopSignal,
		itemOnBuild, itemHealthCheck, itemShell, itemDo, itemCommand,
		itemFunctionKW, itemImport, itemCache, itemHost, itemProject,
		itemWith, itemIf, itemFor, itemWait, itemTry:
		return true
	case itemError, itemEOF, itemNL, itemIndent, itemDedent, itemWS, itemComment,
		itemEOLComment, itemElseIf, itemElse, itemEnd, itemVersion, itemDocker,
		itemCatch, itemFinally, itemTarget, itemUserCommand, itemFunction,
		itemAtom, itemEquals:
		return false
	}

	return false
}

// parseVersion parses a VERSION command and its arguments.
func (p *parser) parseVersion() (Version, error) {
	var v Version

	token := p.next() // consume itemVersion
	v.SourceLocation = &SourceLocation{
		StartLine:   token.Line,
		StartColumn: token.Col,
	}

	for {
		tok := p.peek()
		// Only a specific subset of tokens is valid in the VERSION command;
		// all others fall through to default error handling.
		//nolint:exhaustive
		switch tok.Typ {
		case itemAtom:
			p.next()

			v.Args = append(v.Args, tok.Val)
		case itemWS:
			p.next()
			// ignore whitespace between args
		case itemNL, itemComment, itemEOLComment:
			p.next()

			v.SourceLocation.EndLine = tok.Line
			v.SourceLocation.EndColumn = tok.Col

			return v, nil
		case itemEOF:
			v.SourceLocation.EndLine = tok.Line
			v.SourceLocation.EndColumn = tok.Col

			return v, nil
		default:
			p.next()
			return v, p.errorf(tok.pos, "unexpected token in VERSION command: %s", tok.Val)
		}
	}
}

// parseTarget parses a target and its recipe block.
func (p *parser) parseTarget() (Target, error) {
	var target Target

	tok := p.next() // consume itemTarget
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
		// The default block acts as a catch-all for any token types that
		// are not valid statements inside a recipe block.
		//nolint:exhaustive
		switch tok.Typ {
		case itemError:
			p.next()
			return block, p.errorf(tok.pos, "%s", tok.Val)
		case itemDedent, itemEOF, itemEnd, itemElseIf, itemElse, itemCatch, itemFinally:
			return block, nil
		case itemNL, itemWS, itemEOLComment, itemIndent:
			p.next()
			continue
		case itemComment:
			tok = p.next()
			pendingDocsTokens = append(pendingDocsTokens, tok.Val)

			continue
		case itemIf:
			pendingDocsTokens = nil

			ifStmt, err := p.parseIf()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{If: &ifStmt})
		case itemFor:
			pendingDocsTokens = nil

			forStmt, err := p.parseFor()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{For: &forStmt})
		case itemWait:
			pendingDocsTokens = nil

			waitStmt, err := p.parseWait()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{Wait: &waitStmt})
		case itemTry:
			pendingDocsTokens = nil

			tryStmt, err := p.parseTry()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{Try: &tryStmt})
		case itemWith:
			pendingDocsTokens = nil

			withStmt, err := p.parseWith()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{With: &withStmt})
		case itemFrom, itemFromDockerfile, itemLocally, itemCopy, itemSaveArtifact,
			itemSaveImage, itemRun, itemExpose, itemVolume, itemEnv, itemArg,
			itemSet, itemLet, itemLabel, itemBuild, itemWorkdir, itemUser,
			itemCmd, itemEntrypoint, itemGitClone, itemAdd, itemStopSignal,
			itemOnBuild, itemHealthCheck, itemShell, itemDo, itemCommand,
			itemFunctionKW, itemImport, itemVersion, itemCache, itemHost,
			itemProject:
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
		case itemTarget, itemUserCommand:
			return block, p.errorf(tok.pos, "unexpected token in recipe block: type %d (%s)", tok.Typ, tok.Val)
		default:
			return block, p.errorf(tok.pos, "unexpected token in recipe block: type %d (%s)", tok.Typ, tok.Val)
		}
	}
}

func (p *parser) parseBlock() (Block, error) {
	var (
		block             = make(Block, 0, 8)
		pendingDocsTokens []string
	)

	sawNL := false

	// Expect optional NLs/Comments and then an Indent
	for {
		t := p.peek()
		if t.Typ == itemNL || t.Typ == itemWS || t.Typ == itemEOLComment {
			p.next()

			continue
		}

		if t.Typ == itemComment {
			t = p.next()
			pendingDocsTokens = append(pendingDocsTokens, t.Val)

			continue
		}

		if t.Typ == itemIndent {
			p.next() // consume indent

			break
		}

		if t.Typ == itemEOF || t.Typ == itemTarget || t.Typ == itemUserCommand || t.Typ == itemFunction {
			// empty block
			return nil, nil
		}

		return nil, p.errorf(t.pos, "expected block indentation, got %s", t.Val)
	}

	for {
		tok := p.peek()

		// The default block is a generic catch-all for all token types that
		// cannot start a statement within a block.
		//nolint:exhaustive
		switch tok.Typ {
		case itemError:
			p.next()

			return block, p.errorf(tok.pos, "%s", tok.Val)

		case itemDedent, itemEOF:
			if tok.Typ == itemDedent {
				p.next()
			}

			if len(block) == 0 {
				return nil, nil
			}

			return block, nil

		case itemNL, itemWS, itemEOLComment:
			tok = p.next()
			if tok.Typ == itemNL {
				if sawNL {
					pendingDocsTokens = nil
					sawNL = false
				} else {
					sawNL = true
				}
			}

			continue

		case itemComment:
			tok = p.next()
			pendingDocsTokens = append(pendingDocsTokens, tok.Val)
			sawNL = false

			continue

		case itemIf:
			sawNL = false
			pendingDocsTokens = nil

			ifStmt, err := p.parseIf()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{If: &ifStmt})

		case itemFor:
			sawNL = false
			pendingDocsTokens = nil

			forStmt, err := p.parseFor()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{For: &forStmt})

		case itemTry:
			sawNL = false
			pendingDocsTokens = nil

			tryStmt, err := p.parseTry()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{Try: &tryStmt})

		case itemWith:
			sawNL = false

			withStmt, err := p.parseWith()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{With: &withStmt})

		case itemWait:
			sawNL = false
			pendingDocsTokens = nil

			waitStmt, err := p.parseWait()
			if err != nil {
				return block, err
			}

			block = append(block, Statement{Wait: &waitStmt})

		case itemFrom, itemFromDockerfile, itemLocally, itemCopy, itemSaveArtifact,
			itemSaveImage, itemRun, itemExpose, itemVolume, itemEnv, itemArg,
			itemSet, itemLet, itemLabel, itemBuild, itemWorkdir, itemUser,
			itemCmd, itemEntrypoint, itemGitClone, itemAdd, itemStopSignal,
			itemOnBuild, itemHealthCheck, itemShell, itemDo, itemCommand,
			itemFunctionKW, itemImport, itemVersion, itemCache, itemHost,
			itemProject:
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
			return block, p.errorf(tok.pos, "unexpected token in recipe block: type %d (%s)", tok.Typ, tok.Val)
		}
	}
}

func (p *parser) parseCommand() (Command, error) {
	var cmd Command

	tok := p.next()
	cmd.Name = Cmd(tok.Val)
	cmd.SourceLocation = &SourceLocation{
		StartLine:   tok.Line,
		StartColumn: tok.Col,
	}

	var args []string

	var endLoc SourceLocation

	var err error

	//nolint:exhaustive // Only ENV/ARG/SET/LET are parsed specially; other commands parse until newline.
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
		//nolint:exhaustive // Only commands supporting list/exec form or split args (LABEL) require post-processing.
		switch cmd.Name {
		case CmdRun, CmdCmd, CmdEntrypoint, CmdVolume:
			if hasPrefixBracket(cmd.Args[0]) && hasSuffixBracket(cmd.Args[len(cmd.Args)-1]) {
				joined := strings.Join(cmd.Args, "")
				if execArgs, ok := parseExecForm(joined); ok {
					cmd.Args = execArgs
					cmd.ExecMode = true
				}
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
	var args []string

	var endLoc SourceLocation

	for {
		t := p.peek()
		// Arguments only allow a specific subset of token types; all other
		// tokens are invalid and handled by default.
		//nolint:exhaustive
		switch t.Typ {
		case itemAtom:
			p.next()

			if args == nil {
				args = make([]string, 0, 4)
			}

			args = append(args, t.Val)
		case itemWS, itemComment:
			p.next()
			// ignore
		case itemNL, itemEOLComment:
			p.next()

			endLoc.EndLine = t.Line
			endLoc.EndColumn = t.Col

			return args, endLoc, nil
		case itemEOF, itemDedent:
			// DO NOT consume EOF or Dedent!
			endLoc.EndLine = t.Line
			endLoc.EndColumn = t.Col

			return args, endLoc, nil
		default:
			p.next()

			if t.Typ == itemError {
				return nil, endLoc, p.errorf(t.pos, "%s", t.Val)
			}

			return nil, endLoc, p.errorf(t.pos, "unexpected token in args: %s", t.Val)
		}
	}
}

func (p *parser) parseKeyValueCommandArgs() ([]string, SourceLocation, error) {
	var args []string

	var endLoc SourceLocation

	p.itemsBuf = p.itemsBuf[:0]

	for {
		t := p.peek()
		if t.Typ == itemNL || t.Typ == itemEOLComment {
			p.next() // consume NL or EOL comment

			endLoc.EndLine = t.Line
			endLoc.EndColumn = t.Col

			break
		}

		if t.Typ == itemEOF || t.Typ == itemDedent {
			// DO NOT consume EOF or Dedent!
			endLoc.EndLine = t.Line
			endLoc.EndColumn = t.Col

			break
		}

		if t.Typ == itemError {
			p.next()

			return nil, endLoc, p.errorf(t.pos, "%s", t.Val)
		}

		p.next()

		p.itemsBuf = append(p.itemsBuf, t)
	}

	args = parseKeyValueItems(p.itemsBuf)

	return args, endLoc, nil
}

func parseKeyValueItems(items []item) []string {
	var args []string

	idx := 0
	// Skip leading WS
	for idx < len(items) && items[idx].Typ == itemWS {
		idx++
	}

	// Read flags
	for idx < len(items) {
		if items[idx].Typ == itemAtom && strings.HasPrefix(items[idx].Val, "-") {
			if args == nil {
				args = make([]string, 0, 4)
			}

			args = append(args, items[idx].Val)
			idx++
			// skip WS after flag
			for idx < len(items) && items[idx].Typ == itemWS {
				idx++
			}
		} else {
			break
		}
	}

	// Now we expect the key. If key is missing, fallback:
	if idx >= len(items) || items[idx].Typ != itemAtom {
		for _, item := range items {
			if item.Typ == itemAtom {
				if args == nil {
					args = make([]string, 0, 4)
				}

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

	eqIdx := findKeyValueSeparator(keyToken)
	if eqIdx != -1 {
		key = keyToken[:eqIdx]
		hasEquals = true

		if eqIdx+1 < len(keyToken) {
			valStart = keyToken[eqIdx+1:]
		}
	} else {
		key = keyToken
	}

	// skip WS after key only if we haven't found the equals sign in the key token
	if !hasEquals {
		for idx < len(items) && items[idx].Typ == itemWS {
			idx++
		}
	}

	if args == nil {
		args = make([]string, 0, 4)
	}

	args = append(args, key)

	// If there wasn't an equals sign in the key token itself, check the next token
	if !hasEquals {
		if idx < len(items) && items[idx].Typ == itemAtom && items[idx].Val == "=" {
			args = append(args, "=")
			hasEquals = true
			idx++
			// skip WS after '='
			for idx < len(items) && items[idx].Typ == itemWS {
				idx++
			}
		}
	} else {
		args = append(args, "=")
	}

	// Everything else is the value!
	var val string

	switch {
	case idx >= len(items):
		val = valStart

	case valStart == "" && idx == len(items)-1:
		val = items[idx].Val

	default:
		var valBuilder strings.Builder
		if valStart != "" {
			valBuilder.WriteString(valStart)
		}

		for i := idx; i < len(items); i++ {
			valBuilder.WriteString(items[i].Val)
		}

		val = valBuilder.String()
	}

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
		// Only control flow tokens (ELSE IF, ELSE, END, DEDENT) are expected;
		// any other token is a syntax error handled by default.
		//nolint:exhaustive
		switch tok.Typ {
		case itemDedent:
			p.next() // consume dedent
		case itemElseIf:
			p.next() // consume itemElseIf

			args, endLoc, err := p.parseArgsUntilNL()
			if err != nil {
				return ifStmt, err
			}

			eiBlock, err := p.parseStmts()
			if err != nil {
				return ifStmt, err
			}

			ifStmt.ElseIf = append(ifStmt.ElseIf, ElseIfStatement{
				SourceLocation: &SourceLocation{
					StartLine:   tok.Line,
					StartColumn: tok.Col,
					EndLine:     endLoc.EndLine,
					EndColumn:   endLoc.EndColumn,
				},
				Expression: args,
				Body:       eiBlock,
			})
		case itemElse:
			p.next() // consume itemElse

			_, _, err := p.parseArgsUntilNL() // parse till NL
			if err != nil {
				return ifStmt, err
			}

			eBlock, err := p.parseStmts()
			if err != nil {
				return ifStmt, err
			}

			ifStmt.ElseBody = &eBlock
		case itemEnd:
			p.next() // consume END

			_, _, err := p.parseArgsUntilNL()
			if err != nil {
				return ifStmt, err
			}

			return ifStmt, nil
		default:
			return ifStmt, p.errorf(tok.pos, "expected END to close IF statement, got %s", tok.Val)
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

	if p.peek().Typ == itemDedent {
		p.next()
	}

	tok := p.peek()
	if tok.Typ != itemEnd {
		return forStmt, p.errorf(tok.pos, "expected END to close FOR statement, got %s", tok.Val)
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

		// Only control flow tokens (CATCH, FINALLY, END, DEDENT) are expected;
		// any other token is a syntax error handled by default.
		//nolint:exhaustive
		switch tok.Typ {
		case itemDedent:
			p.next()
		case itemCatch:
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
		case itemFinally:
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
		case itemEnd:
			p.next()

			_, _, err := p.parseArgsUntilNL()
			if err != nil {
				return tryStmt, err
			}

			return tryStmt, nil
		default:
			return tryStmt, p.errorf(tok.pos, "expected END to close TRY statement, got %s", tok.Val)
		}
	}
}

func (p *parser) parseWith() (WithStatement, error) {
	withStmt := WithStatement{}

	t := p.next()
	withStmt.SourceLocation = &SourceLocation{StartLine: t.Line, StartColumn: t.Col}

	for p.peek().Typ == itemWS {
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

	if p.peek().Typ == itemDedent {
		p.next()
	}

	tok := p.peek()
	if tok.Typ != itemEnd {
		return withStmt, p.errorf(tok.pos, "expected END to close WITH statement, got %s", tok.Val)
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

	if p.peek().Typ == itemDedent {
		p.next()
	}

	tok := p.peek()
	if tok.Typ != itemEnd {
		return waitStmt, p.errorf(tok.pos, "expected END to close WAIT statement, got %s", tok.Val)
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

func decodeJSONEscape(escaped byte, sb *strings.Builder) {
	switch escaped {
	case '"', '\\', '/':
		sb.WriteByte(escaped)

	case 'b':
		sb.WriteByte('\b')

	case 'f':
		sb.WriteByte('\f')

	case 'n':
		sb.WriteByte('\n')

	case 'r':
		sb.WriteByte('\r')

	case 't':
		sb.WriteByte('\t')

	default:
		sb.WriteByte('\\')
		sb.WriteByte(escaped)
	}
}

func parseExecForm(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, false
	}

	s = s[1 : len(s)-1]

	var (
		args    []string
		current strings.Builder
	)

	inString := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if inString {
			switch c {
			case '"':
				inString = false

				args = append(args, current.String())
				current.Reset()

			case '\\':
				i++
				if i >= len(s) {
					return nil, false
				}

				decodeJSONEscape(s[i], &current)

			default:
				current.WriteByte(c)
			}
		} else {
			switch c {
			case '"':
				inString = true

			case ',', ' ', '\t', '\r', '\n':
				continue

			default:
				return nil, false
			}
		}
	}

	if inString {
		return nil, false
	}

	return args, true
}

func findKeyValueSeparator(s string) int {
	inSingle := false
	inDouble := false

	for i := range len(s) {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle

		case c == '"' && !inSingle:
			inDouble = !inDouble

		case c == '=' && !inSingle && !inDouble:
			return i
		}
	}

	return -1
}

func splitKeyValueArg(s string) []string {
	idx := findKeyValueSeparator(s)
	if idx == -1 {
		if s == "" {
			return nil
		}

		return []string{s}
	}

	out := make([]string, 0, 3)

	if idx > 0 {
		out = append(out, s[:idx])
	}

	out = append(out, "=")

	if idx+1 < len(s) {
		out = append(out, s[idx+1:])
	}

	return out
}

// matchLineContinuationFrom matches a backslash line continuation starting at index i of the string s.
// It returns the length of the matched line continuation sequence (including comments and nested continuations)
// or 0 if it does not match.
func matchLineContinuationFrom(s string, i int) int {
	if i >= len(s) || s[i] != '\\' {
		return 0
	}

	pos := i + 1

	// Consume [ \t]*
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
		pos++
	}

	// Consume optional comment (#[^\n\r]*)?
	if pos < len(s) && s[pos] == '#' {
		pos++
		for pos < len(s) && s[pos] != '\n' && s[pos] != '\r' {
			pos++
		}
	}

	// Consume newline: (\n|(\r\n))
	switch {
	case pos < len(s) && s[pos] == '\n':
		pos++
	case pos+1 < len(s) && s[pos] == '\r' && s[pos+1] == '\n':
		pos += 2
	default:
		return 0
	}

	// Consume [ \t]*
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
		pos++
	}

	// Consume ((#[^\n\r]*)?(\n|(\r\n))[\t ]*)*
loop:
	for {
		start := pos
		// Optional comment
		if pos < len(s) && s[pos] == '#' {
			pos++
			for pos < len(s) && s[pos] != '\n' && s[pos] != '\r' {
				pos++
			}
		}

		// Newline
		switch {
		case pos < len(s) && s[pos] == '\n':
			pos++
		case pos+1 < len(s) && s[pos] == '\r' && s[pos+1] == '\n':
			pos += 2
		default:
			pos = start
			break loop
		}

		// Spaces/tabs
		for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
			pos++
		}
	}

	return pos - i
}

func replaceEscape(str string) string {
	if !strings.ContainsAny(str, "\\'\"") {
		return str
	}

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
				if n := matchLineContinuationFrom(str, i); n > 0 {
					i += n
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

func hasPrefixBracket(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			continue
		}

		return c == '['
	}

	return false
}

func hasSuffixBracket(s string) bool {
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			continue
		}

		return c == ']'
	}

	return false
}
