package parse

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/ast/spec"
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

// backup backs the input stream up one token.
func (p *parser) backup() {
	p.peekCount++
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
func (p *parser) errorf(pos Pos, format string, args ...interface{}) error {
	p.lex.drain()
	return fmt.Errorf("parse error at pos %d: %s", pos, fmt.Sprintf(format, args...))
}

// expect consumes the next token and guarantees it has the required type.
func (p *parser) expect(expected ItemType, context string) (Item, error) {
	token := p.next()
	if token.Typ != expected {
		return token, p.errorf(token.Pos, "expected %v in %s, got %v", expected, context, token.Typ)
	}
	return token, nil
}

// Parse parses the Earthfile text and returns an AST.
func Parse(name, text string) (spec.Earthfile, error) {
	p := &parser{
		lex: lex(name, text),
	}
	return p.parseEarthfile()
}

// parseEarthfile is the top-level entry point for recursive descent.
func (p *parser) parseEarthfile() (spec.Earthfile, error) {
	var ef spec.Earthfile
	
	for {
		token := p.peek()
		switch token.Typ {
		case ItemEOF:
			p.next() // consume EOF
			return ef, nil
		case ItemNL, ItemWS, ItemComment:
			p.next() // skip top-level whitespace and comments
		case ItemVersion:
			version, err := p.parseVersion()
			if err != nil {
				return ef, err
			}
			ef.Version = &version
		case ItemTarget:
			target, err := p.parseTarget()
			if err != nil {
				return ef, err
			}
			ef.Targets = append(ef.Targets, target)
		case ItemFunction, ItemUserCommand:
			fn, err := p.parseFunction()
			if err != nil {
				return ef, err
			}
			ef.Functions = append(ef.Functions, fn)
		default:
			return ef, p.errorf(token.Pos, "unexpected token at top level: %s", token.Val)
		}
	}
}

// parseVersion parses a VERSION command and its arguments.
func (p *parser) parseVersion() (spec.Version, error) {
	var v spec.Version
	token := p.next() // consume ItemVersion
	v.SourceLocation = &spec.SourceLocation{
		StartLine:   token.Line,
		StartColumn: token.Col,
	}

	for {
		tok := p.next()
		switch tok.Typ {
		case ItemAtom:
			v.Args = append(v.Args, tok.Val)
		case ItemWS:
			// ignore whitespace between args
		case ItemNL, ItemEOF:
			v.SourceLocation.EndLine = tok.Line
			v.SourceLocation.EndColumn = tok.Col
			return v, nil
		default:
			return v, p.errorf(tok.Pos, "unexpected token in VERSION command: %s", tok.Val)
		}
	}
}

// parseTarget parses a target and its recipe block.
func (p *parser) parseTarget() (spec.Target, error) {
	var target spec.Target
	tok := p.next() // consume ItemTarget
	target.Name = tok.Val
	target.SourceLocation = &spec.SourceLocation{
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

func (p *parser) parseFunction() (spec.Function, error) {
	fn := spec.Function{
		Name: p.peek().Val,
		SourceLocation: &spec.SourceLocation{
			StartLine:   p.peek().Line,
			StartColumn: p.peek().Col,
		},
	}
	p.next() // consume
	block, err := p.parseBlock()
	fn.Recipe = block
	return fn, err
}

func (p *parser) parseBlock() (spec.Block, error) {
	var block spec.Block
	// Expect optional NLs/Comments and then an Indent
	for {
		tok := p.peek()
		if tok.Typ == ItemNL || tok.Typ == ItemWS || tok.Typ == ItemComment {
			p.next()
			continue
		}
		if tok.Typ == ItemIndent {
			p.next() // consume indent
			break
		}
		if tok.Typ == ItemEOF || tok.Typ == ItemTarget || tok.Typ == ItemUserCommand {
			// empty block
			return block, nil
		}
		return block, p.errorf(tok.Pos, "expected block indentation, got %s", tok.Val)
	}

	for {
		tok := p.peek()
		switch tok.Typ {
		case ItemDedent, ItemEOF:
			if tok.Typ == ItemDedent {
				p.next()
			}
			return block, nil
		case ItemNL, ItemWS, ItemComment:
			p.next()
			continue
		case ItemIf:
			ifStmt, err := p.parseIf()
			if err != nil {
				return block, err
			}
			block = append(block, spec.Statement{If: &ifStmt})
		case ItemFor:
			forStmt, err := p.parseFor()
			if err != nil {
				return block, err
			}
			block = append(block, spec.Statement{For: &forStmt})
		case ItemTry:
			tryStmt, err := p.parseTry()
			if err != nil {
				return block, err
			}
			block = append(block, spec.Statement{Try: &tryStmt})
		case ItemWith:
			withStmt, err := p.parseWith()
			if err != nil {
				return block, err
			}
			block = append(block, spec.Statement{With: &withStmt})
		case ItemWait:
			waitStmt, err := p.parseWait()
			if err != nil {
				return block, err
			}
			block = append(block, spec.Statement{Wait: &waitStmt})
		case ItemRun, ItemFrom, ItemWorkdir, ItemCopy:
			cmd, err := p.parseCommand()
			if err != nil {
				return block, err
			}
			block = append(block, spec.Statement{Command: &cmd})
		default:
			return block, p.errorf(tok.Pos, "unexpected token in recipe block: %s", tok.Val)
		}
	}
}

func (p *parser) parseCommand() (spec.Command, error) {
	var cmd spec.Command
	tok := p.next()
	cmd.Name = tok.Val
	cmd.SourceLocation = &spec.SourceLocation{
		StartLine:   tok.Line,
		StartColumn: tok.Col,
	}

	args, endLoc, err := p.parseArgsUntilNL()
	if err != nil {
		return cmd, err
	}
	cmd.Args = args
	cmd.SourceLocation.EndLine = endLoc.EndLine
	cmd.SourceLocation.EndColumn = endLoc.EndColumn
	return cmd, nil
}

func (p *parser) parseArgsUntilNL() ([]string, spec.SourceLocation, error) {
	var args []string
	var endLoc spec.SourceLocation
	for {
		t := p.next()
		switch t.Typ {
		case ItemAtom:
			args = append(args, t.Val)
		case ItemWS:
			// ignore
		case ItemNL, ItemEOF, ItemDedent, ItemComment:
			endLoc.EndLine = t.Line
			endLoc.EndColumn = t.Col
			return args, endLoc, nil
		default:
			return nil, endLoc, p.errorf(t.Pos, "unexpected token in args: %s", t.Val)
		}
	}
}

func (p *parser) parseIf() (spec.IfStatement, error) {
	ifStmt := spec.IfStatement{}
	
	t := p.next()
	ifStmt.SourceLocation = &spec.SourceLocation{StartLine: t.Line, StartColumn: t.Col}
	
	args, endLoc, err := p.parseArgsUntilNL()
	if err != nil {
		return ifStmt, err
	}
	ifStmt.Expression = args
	ifStmt.SourceLocation.EndLine = endLoc.EndLine
	ifStmt.SourceLocation.EndColumn = endLoc.EndColumn
	
	block, err := p.parseBlock()
	if err != nil {
		return ifStmt, err
	}
	ifStmt.IfBody = block
	
	for {
		tok := p.peek()
		switch tok.Typ {
		case ItemElseIf:
			p.next() // consume ItemElseIf
			args, endLoc, err := p.parseArgsUntilNL()
			if err != nil {
				return ifStmt, err
			}
			eiBlock, err := p.parseBlock()
			if err != nil {
				return ifStmt, err
			}
			ifStmt.ElseIf = append(ifStmt.ElseIf, spec.ElseIf{
				SourceLocation: &spec.SourceLocation{StartLine: tok.Line, StartColumn: tok.Col, EndLine: endLoc.EndLine, EndColumn: endLoc.EndColumn},
				Expression:     args,
				Body:           eiBlock,
			})
		case ItemElse:
			p.next() // consume ItemElse
			_, _, err := p.parseArgsUntilNL() // parse till NL
			if err != nil {
				return ifStmt, err
			}
			eBlock, err := p.parseBlock()
			if err != nil {
				return ifStmt, err
			}
			ifStmt.ElseBody = &eBlock
		case ItemEnd:
			p.next() // consume END
			p.parseArgsUntilNL() // parse till NL
			return ifStmt, nil
		default:
			// No more clauses. Return the IF statement.
			return ifStmt, nil
		}
	}
}

func (p *parser) parseFor() (spec.ForStatement, error) {
	forStmt := spec.ForStatement{}
	
	t := p.next()
	forStmt.SourceLocation = &spec.SourceLocation{StartLine: t.Line, StartColumn: t.Col}
	
	args, _, err := p.parseArgsUntilNL()
	if err != nil {
		return forStmt, err
	}
	forStmt.Args = args
	
	block, err := p.parseBlock()
	if err != nil {
		return forStmt, err
	}
	forStmt.Body = block
	
	if tok := p.peek(); tok.Typ == ItemEnd {
		p.next()
		p.parseArgsUntilNL()
	}
	
	return forStmt, nil
}

func (p *parser) parseTry() (spec.TryStatement, error) {
	tryStmt := spec.TryStatement{}
	
	t := p.next()
	tryStmt.SourceLocation = &spec.SourceLocation{StartLine: t.Line, StartColumn: t.Col}
	p.parseArgsUntilNL()
	
	block, err := p.parseBlock()
	if err != nil {
		return tryStmt, err
	}
	tryStmt.TryBody = block
	
	for {
		tok := p.peek()
		switch tok.Typ {
		case ItemCatch:
			p.next()
			p.parseArgsUntilNL()
			catchBlock, err := p.parseBlock()
			if err != nil {
				return tryStmt, err
			}
			tryStmt.CatchBody = &catchBlock
		case ItemFinally:
			p.next()
			p.parseArgsUntilNL()
			finallyBlock, err := p.parseBlock()
			if err != nil {
				return tryStmt, err
			}
			tryStmt.FinallyBody = &finallyBlock
		case ItemEnd:
			p.next()
			p.parseArgsUntilNL()
			return tryStmt, nil
		default:
			return tryStmt, nil
		}
	}
}

func (p *parser) parseWith() (spec.WithStatement, error) {
	withStmt := spec.WithStatement{}
	
	t := p.next()
	withStmt.SourceLocation = &spec.SourceLocation{StartLine: t.Line, StartColumn: t.Col}
	
	for p.peek().Typ == ItemWS {
		p.next()
	}
	
	cmd, err := p.parseCommand()
	if err != nil {
		return withStmt, err
	}
	withStmt.Command = cmd
	
	block, err := p.parseBlock()
	if err != nil {
		return withStmt, err
	}
	withStmt.Body = block
	
	if tok := p.peek(); tok.Typ == ItemEnd {
		p.next()
		p.parseArgsUntilNL()
	}
	
	return withStmt, nil
}

func (p *parser) parseWait() (spec.WaitStatement, error) {
	waitStmt := spec.WaitStatement{}
	
	t := p.next()
	waitStmt.SourceLocation = &spec.SourceLocation{StartLine: t.Line, StartColumn: t.Col}
	p.parseArgsUntilNL()
	
	block, err := p.parseBlock()
	if err != nil {
		return waitStmt, err
	}
	waitStmt.Body = block
	
	if tok := p.peek(); tok.Typ == ItemEnd {
		p.next()
		p.parseArgsUntilNL()
	}
	
	return waitStmt, nil
}
