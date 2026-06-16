package parse

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ItemType identifies the type of lex items.
type ItemType int

const (
	ItemError ItemType = iota // error occurred; value is text of error
	ItemEOF
	ItemNL     // newline
	ItemIndent // indentation increase
	ItemDedent // indentation decrease
	ItemWS     // whitespace
	ItemComment // comment

	// Keywords
	ItemFrom
	ItemFromDockerfile
	ItemLocally
	ItemCopy
	ItemSaveArtifact
	ItemSaveImage
	ItemRun
	ItemExpose
	ItemVolume
	ItemEnv
	ItemArg
	ItemSet
	ItemLet
	ItemLabel
	ItemBuild
	ItemWorkdir
	ItemIf
	ItemElseIf
	ItemElse
	ItemEnd
	ItemCmd
	ItemEntrypoint
	ItemGitClone
	ItemAdd
	ItemStopSignal
	ItemOnBuild
	ItemHealthCheck
	ItemShell
	ItemDo
	ItemCommand
	ItemFunctionKW
	ItemImport
	ItemVersion
	ItemCache
	ItemHost
	ItemProject

	// Block keywords
	ItemWith
	ItemDocker
	ItemTry
	ItemCatch
	ItemFinally
	ItemFor
	ItemWait

	// Other structures
	ItemTarget
	ItemUserCommand
	ItemFunction
	ItemAtom
	ItemEquals
)

const eof = -1

// Pos represents a byte position in the original input text.
type Pos int

// Item represents a token or text string returned from the scanner.
type Item struct {
	Typ  ItemType
	Pos  Pos
	Val  string
	Line int
	Col  int
}

func (i Item) String() string {
	switch {
	case i.Typ == ItemEOF:
		return "EOF"
	case i.Typ == ItemError:
		return i.Val
	case len(i.Val) > 10:
		return fmt.Sprintf("%.10q...", i.Val)
	}
	return fmt.Sprintf("%q", i.Val)
}

// stateFn represents the state of the scanner as a function that returns the next state.
type stateFn func(*lexer) stateFn

// lexer holds the state of the scanner.
type lexer struct {
	name       string    // the name of the input; used only for error reports
	input      string    // the string being scanned
	state      stateFn   // the next lexing function to enter
	pos        Pos       // current position in the input
	start      Pos       // start position of this item
	width      Pos       // width of last rune read from input
	lastPos    Pos       // position of most recent item returned by nextItem
	items      chan Item // channel of scanned items
	line       int       // 1-based line number
	col        int       // 1-based column number
	startLine  int
	startCol   int
	prevLine   int
	prevCol    int
	indentStk  []int     // track indentation levels
}

// next returns the next rune in the input.
func (l *lexer) next() rune {
	if int(l.pos) >= len(l.input) {
		l.width = 0
		return eof
	}
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.width = Pos(w)
	l.pos += l.width

	l.prevLine = l.line
	l.prevCol = l.col

	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

// peek returns but does not consume the next rune in the input.
func (l *lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

// backup steps back one rune. Can only be called once per call of next.
func (l *lexer) backup() {
	l.pos -= l.width
	l.line = l.prevLine
	l.col = l.prevCol
}

// emit passes an item back to the client.
func (l *lexer) emit(t ItemType) {
	l.items <- Item{
		Typ:  t,
		Pos:  l.start,
		Val:  l.input[l.start:l.pos],
		Line: l.startLine,
		Col:  l.startCol,
	}
	l.start = l.pos
	l.startLine = l.line
	l.startCol = l.col
}

// ignore skips over the pending input before this point.
func (l *lexer) ignore() {
	l.start = l.pos
	l.startLine = l.line
	l.startCol = l.col
}

// errorf returns an error token and terminates the scan by passing
// back a nil pointer that will be the next state, terminating l.nextItem.
func (l *lexer) errorf(format string, args ...interface{}) stateFn {
	l.items <- Item{
		Typ:  ItemError,
		Pos:  l.start,
		Val:  fmt.Sprintf(format, args...),
		Line: l.startLine,
		Col:  l.startCol,
	}
	return nil
}

// nextItem returns the next item from the input.
// Called by the parser, not in the lexing goroutine.
func (l *lexer) nextItem() Item {
	item := <-l.items
	l.lastPos = item.Pos
	return item
}

// drain drains the output so the lexing goroutine will exit.
// Called by the parser, not in the lexing goroutine.
func (l *lexer) drain() {
	for range l.items {
	}
}

// lex creates a new scanner for the input string.
func lex(name, input string) *lexer {
	l := &lexer{
		name:      name,
		input:     input,
		items:     make(chan Item, 2),
		line:      1,
		col:       1,
		prevLine:  1,
		prevCol:   1,
		startLine: 1,
		startCol:  1,
		indentStk: []int{0},
	}
	go l.run()
	return l
}

// run runs the state machine for the lexer.
func (l *lexer) run() {
	for l.state = lexDefault; l.state != nil; {
		l.state = l.state(l)
	}
	close(l.items)
}

// isSpace reports whether r is a space character.
func isSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

// isEndOfLine reports whether r is an end-of-line character.
func isEndOfLine(r rune) bool {
	return r == '\r' || r == '\n'
}

// isAlphaNumeric reports whether r is an alphabetic, digit, or underscore.
func isAlphaNumeric(r rune) bool {
	return r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
}

// lexDefault is the starting state.
func lexDefault(l *lexer) stateFn {
	for {
		r := l.peek()
		switch {
		case r == eof:
			l.emit(ItemEOF)
			return nil
		case isSpace(r):
			return lexSpace
		case isEndOfLine(r):
			return lexNL
		case r == '#':
			return lexComment
		default:
			// It might be a target, user command, function, or version.
			return lexIdentifier
		}
	}
}

func lexSpace(l *lexer) stateFn {
	for isSpace(l.peek()) {
		l.next()
	}
	l.emit(ItemWS)
	return lexDefault
}

func lexNL(l *lexer) stateFn {
	r := l.next()
	if r == '\r' && l.peek() == '\n' {
		l.next()
	}
	l.emit(ItemNL)
	// Indentation is tracked when we are in RECIPE mode.
	// For now, return to default.
	return lexDefault
}

func lexComment(l *lexer) stateFn {
	l.next() // consume '#'
	for {
		r := l.peek()
		if isEndOfLine(r) || r == eof {
			break
		}
		l.next()
	}
	l.emit(ItemComment)
	return lexDefault
}

func lexIdentifier(l *lexer) stateFn {
	// A target starts with [a-z], user command/function with [A-Z].
	r := l.next()
	if !isAlphaNumeric(r) && r != '-' && r != '.' {
		return l.errorf("unexpected character in identifier: %#U", r)
	}
	
	for {
		r = l.peek()
		if isAlphaNumeric(r) || r == '-' || r == '.' {
			l.next()
		} else {
			break
		}
	}
	
	val := l.input[l.start:l.pos]
	if val == "VERSION" {
		l.emit(ItemVersion)
		return lexGlobalCommandArgs
	}
	if val == "PROJECT" {
		l.emit(ItemProject)
		return lexGlobalCommandArgs
	}

	if val == "COMMAND" || val == "FUNCTION" {
		if isSpace(l.peek()) {
			l.next()
			for isAlphaNumeric(l.peek()) || l.peek() == '-' || l.peek() == '_' {
				l.next()
			}
			if l.peek() == ':' {
				l.next()
			}
			if val == "COMMAND" {
				l.emit(ItemUserCommand)
			} else {
				l.emit(ItemFunction)
			}
			l.indentStk = []int{0}
			return lexRecipe
		}
	}

	if l.peek() == ':' {
		// It's a target, user command, or function.
		l.next() // consume ':'
		val = l.input[l.start:l.pos]
		if 'a' <= val[0] && val[0] <= 'z' {
			l.emit(ItemTarget)
		} else {
			l.emit(ItemTarget) // Fallback if the first char isn't a-z, it's still a target
		}
		// Reset indent tracking for the new target
		l.indentStk = []int{0}
		return lexRecipe
	}

	return l.errorf("expected ':' after identifier %q", val)
}

func lexRecipe(l *lexer) stateFn {
	// First, check for indentation changes at the start of a line
	if l.col == 1 {
		indent := 0
		for isSpace(l.peek()) {
			l.next()
			indent++
		}
		if l.pos > l.start {
			l.ignore() // we don't emit WS for indentation, we emit ItemIndent/Dedent
		}
		
		currentIndent := l.indentStk[len(l.indentStk)-1]
		if indent > currentIndent {
			l.indentStk = append(l.indentStk, indent)
			l.emit(ItemIndent)
		} else if indent < currentIndent {
			// dedent until we match
			for len(l.indentStk) > 1 && l.indentStk[len(l.indentStk)-1] > indent {
				l.indentStk = l.indentStk[:len(l.indentStk)-1]
				l.emit(ItemDedent)
			}
			if l.indentStk[len(l.indentStk)-1] != indent {
				return l.errorf("inconsistent indentation")
			}
			if indent == 0 {
				return lexDefault // Dedented back to top level
			}
		}
	}

	r := l.peek()
	switch {
	case r == eof:
		// Dedent everything
		for len(l.indentStk) > 1 {
			l.indentStk = l.indentStk[:len(l.indentStk)-1]
			l.emit(ItemDedent)
		}
		l.emit(ItemEOF)
		return nil
	case isSpace(r):
		for isSpace(l.peek()) {
			l.next()
		}
		l.emit(ItemWS)
		return lexRecipe
	case isEndOfLine(r):
		r = l.next()
		if r == '\r' && l.peek() == '\n' {
			l.next()
		}
		l.emit(ItemNL)
		return lexRecipe
	case r == '#':
		return lexComment
	default:
		// Command keyword like RUN, FROM
		return lexCommandKeyword
	}
}

func lexCommandKeyword(l *lexer) stateFn {
	for {
		r := l.peek()
		if isAlphaNumeric(r) {
			l.next()
		} else {
			break
		}
	}
	
	val := l.input[l.start:l.pos]
	
	if val == "ELSE" {
		// Check for "ELSE IF"
		// We peek ahead to see if it matches exactly " IF" followed by space or EOF/NL.
		if strings.HasPrefix(l.input[l.pos:], " IF") {
			nextCharPos := l.pos + 3
			if int(nextCharPos) >= len(l.input) || isSpace(rune(l.input[nextCharPos])) || isEndOfLine(rune(l.input[nextCharPos])) {
				l.pos = nextCharPos
				l.col += 3
				val = "ELSE IF"
			}
		}
	}

	switch val {
	case "RUN":
		l.emit(ItemRun)
	case "FROM":
		l.emit(ItemFrom)
	case "WORKDIR":
		l.emit(ItemWorkdir)
	case "COPY":
		l.emit(ItemCopy)
	case "IF":
		l.emit(ItemIf)
	case "ELSE IF":
		l.emit(ItemElseIf)
	case "ELSE":
		l.emit(ItemElse)
	case "END":
		l.emit(ItemEnd)
	case "FOR":
		l.emit(ItemFor)
	case "TRY":
		l.emit(ItemTry)
	case "CATCH":
		l.emit(ItemCatch)
	case "FINALLY":
		l.emit(ItemFinally)
	case "WITH":
		l.emit(ItemWith)
	case "WAIT":
		l.emit(ItemWait)
	default:
		return l.errorf("unknown command keyword: %q", val)
	}
	return lexRecipeCommandArgs
}

func lexRecipeCommandArgs(l *lexer) stateFn {
	for {
		r := l.peek()
		switch {
		case r == eof:
			if l.pos > l.start {
				l.emit(ItemAtom)
			}
			l.emit(ItemEOF)
			return nil
		case isSpace(r):
			if l.pos > l.start {
				l.emit(ItemAtom)
			}
			return lexRecipeSpaceArgs
		case isEndOfLine(r):
			if l.pos > l.start {
				l.emit(ItemAtom)
			}
			r = l.next()
			if r == '\r' && l.peek() == '\n' {
				l.next()
			}
			l.emit(ItemNL)
			return lexRecipe
		case r == '\\':
			lexConsumeEscape(l)
		case r == '"':
			l.next()
			if err := lexDoubleQuoteBody(l); err != nil {
				return l.errorf("%v", err)
			}
		case r == '\'':
			l.next()
			if err := lexSingleQuoteBody(l); err != nil {
				return l.errorf("%v", err)
			}
		case r == '$':
			l.next()
			if l.peek() == '(' {
				l.next()
				if err := lexShellOutBody(l); err != nil {
					return l.errorf("%v", err)
				}
			}
		default:
			l.next()
		}
	}
}

func lexRecipeSpaceArgs(l *lexer) stateFn {
	for isSpace(l.peek()) {
		l.next()
	}
	l.emit(ItemWS)
	return lexRecipeCommandArgs
}

func lexGlobalCommandArgs(l *lexer) stateFn {
	for {
		r := l.peek()
		switch {
		case r == eof:
			if l.pos > l.start {
				l.emit(ItemAtom)
			}
			l.emit(ItemEOF)
			return nil
		case isSpace(r):
			if l.pos > l.start {
				l.emit(ItemAtom)
			}
			return lexGlobalSpaceArgs
		case isEndOfLine(r):
			if l.pos > l.start {
				l.emit(ItemAtom)
			}
			return lexNL
		case r == '\\':
			lexConsumeEscape(l)
		case r == '"':
			l.next()
			if err := lexDoubleQuoteBody(l); err != nil {
				return l.errorf("%v", err)
			}
		case r == '\'':
			l.next()
			if err := lexSingleQuoteBody(l); err != nil {
				return l.errorf("%v", err)
			}
		case r == '$':
			l.next()
			if l.peek() == '(' {
				l.next()
				if err := lexShellOutBody(l); err != nil {
					return l.errorf("%v", err)
				}
			}
		default:
			l.next()
		}
	}
}

func lexGlobalSpaceArgs(l *lexer) stateFn {
	for isSpace(l.peek()) {
		l.next()
	}
	l.emit(ItemWS)
	return lexGlobalCommandArgs
}

func lexConsumeEscape(l *lexer) {
	l.next() // consume \
	r := l.next()
	if r == '\r' && l.peek() == '\n' {
		l.next() // consume \n
	}
}

func lexDoubleQuoteBody(l *lexer) error {
	for {
		r := l.next()
		switch r {
		case eof:
			return fmt.Errorf("unclosed double quote")
		case '"':
			return nil // done
		case '\\':
			lexConsumeEscape(l) // consume escaped char
		case '$':
			if l.peek() == '(' {
				l.next() // consume '('
				if err := lexShellOutBody(l); err != nil {
					return err
				}
			}
		}
	}
}

func lexSingleQuoteBody(l *lexer) error {
	for {
		r := l.next()
		switch r {
		case eof:
			return fmt.Errorf("unclosed single quote")
		case '\'':
			return nil
		case '\\':
			lexConsumeEscape(l)
		}
	}
}

func lexShellOutBody(l *lexer) error {
	parenCount := 1
	for parenCount > 0 {
		r := l.next()
		switch r {
		case eof:
			return fmt.Errorf("unclosed shell substitution")
		case '"':
			if err := lexDoubleQuoteBody(l); err != nil {
				return err
			}
		case '\'':
			if err := lexSingleQuoteBody(l); err != nil {
				return err
			}
		case '$':
			if l.peek() == '(' {
				l.next()
				parenCount++
			}
		case ')':
			parenCount--
		case '\\':
			lexConsumeEscape(l)
		}
	}
	return nil
}
