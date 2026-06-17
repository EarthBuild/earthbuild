// Package parse implements the native Earthfile parser.
package parse

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ItemType identifies the type of lex items.
type ItemType int

// ItemType constants.
const (
	// ItemError represents an error.
	ItemError ItemType = iota // error occurred; value is text of error
	ItemEOF
	ItemNL      // newline
	ItemIndent  // indentation increase
	ItemDedent  // indentation decrease
	ItemWS      // whitespace
	ItemComment // comment
	ItemEOLComment

	// Keywords.

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
	ItemUser

	// Block keywords.

	ItemWith
	ItemDocker
	ItemTry
	ItemCatch
	ItemFinally
	ItemFor
	ItemWait

	// Other structures.

	ItemTarget
	ItemUserCommand
	ItemFunction
	ItemAtom
	ItemEquals
)

// Command name string constants.
const (
	CmdAdd            = "ADD"
	CmdArg            = "ARG"
	CmdBuild          = "BUILD"
	CmdCache          = "CACHE"
	CmdCmd            = "CMD"
	CmdCommand        = "COMMAND"
	CmdFunction       = "FUNCTION"
	CmdCopy           = "COPY"
	CmdDo             = "DO"
	CmdDocker         = "DOCKER"
	CmdEntrypoint     = "ENTRYPOINT"
	CmdEnv            = "ENV"
	CmdExpose         = "EXPOSE"
	CmdFrom           = "FROM"
	CmdFromDockerfile = "FROM DOCKERFILE"
	CmdGitClone       = "GIT CLONE"
	CmdHealthCheck    = "HEALTHCHECK"
	CmdHost           = "HOST"
	CmdImport         = "IMPORT"
	CmdLabel          = "LABEL"
	CmdLet            = "LET"
	CmdLoad           = "LOAD"
	CmdLocally        = "LOCALLY"
	CmdOnBuild        = "ONBUILD"
	CmdProject        = "PROJECT"
	CmdRun            = "RUN"
	CmdSaveArtifact   = "SAVE ARTIFACT"
	CmdSaveImage      = "SAVE IMAGE"
	CmdSet            = "SET"
	CmdShell          = "SHELL"
	CmdStopSignal     = "STOPSIGNAL"
	CmdUser           = "USER"
	CmdVolume         = "VOLUME"
	CmdWorkdir        = "WORKDIR"
)

// CommandString returns the canonical command string representation of the ItemType.
func (t ItemType) CommandString() string {
	switch t {
	case ItemAdd:
		return CmdAdd
	case ItemArg:
		return CmdArg
	case ItemBuild:
		return CmdBuild
	case ItemCache:
		return CmdCache
	case ItemCmd:
		return CmdCmd
	case ItemCommand:
		return CmdCommand
	case ItemFunctionKW:
		return CmdFunction
	case ItemCopy:
		return CmdCopy
	case ItemDo:
		return CmdDo
	case ItemDocker:
		return CmdDocker
	case ItemEntrypoint:
		return CmdEntrypoint
	case ItemEnv:
		return CmdEnv
	case ItemExpose:
		return CmdExpose
	case ItemFrom:
		return CmdFrom
	case ItemFromDockerfile:
		return CmdFromDockerfile
	case ItemGitClone:
		return CmdGitClone
	case ItemHealthCheck:
		return CmdHealthCheck
	case ItemHost:
		return CmdHost
	case ItemImport:
		return CmdImport
	case ItemLabel:
		return CmdLabel
	case ItemLet:
		return CmdLet
	case ItemLocally:
		return CmdLocally
	case ItemOnBuild:
		return CmdOnBuild
	case ItemProject:
		return CmdProject
	case ItemRun:
		return CmdRun
	case ItemSaveArtifact:
		return CmdSaveArtifact
	case ItemSaveImage:
		return CmdSaveImage
	case ItemSet:
		return CmdSet
	case ItemShell:
		return CmdShell
	case ItemStopSignal:
		return CmdStopSignal
	case ItemUser:
		return CmdUser
	case ItemVolume:
		return CmdVolume
	case ItemWorkdir:
		return CmdWorkdir
	}

	return ""
}

const eof = -1

// Pos represents a byte position in the original input text.
type Pos int

// Item represents a token or text string returned from the scanner.
type Item struct {
	Val  string
	Typ  ItemType
	Pos  Pos
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
	items         chan Item
	state         stateFn
	input         string
	name          string
	indentStk     []int
	line          int
	lastPos       Pos
	width         Pos
	start         Pos
	col           int
	startLine     int
	startCol      int
	prevLine      int
	prevCol       int
	pos           Pos
	isStartOfLine bool
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

	if t == ItemNL {
		l.isStartOfLine = true
	} else if t != ItemWS && t != ItemComment {
		l.isStartOfLine = false
	}
}

// ignore skips over the pending input before this point.
func (l *lexer) ignore() {
	l.start = l.pos
	l.startLine = l.line
	l.startCol = l.col
}

// errorf returns an error token and terminates the scan by passing
// back a nil pointer that will be the next state, terminating l.nextItem.
func (l *lexer) errorf(format string, args ...any) stateFn {
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
		// Draining items channel to prevent deadlock.
		_ = 0
	}
}

// lex creates a new scanner for the input string.
func lex(name, input string) *lexer {
	l := &lexer{
		name:          name,
		input:         input,
		items:         make(chan Item, 2),
		line:          1,
		col:           1,
		prevLine:      1,
		prevCol:       1,
		startLine:     1,
		startCol:      1,
		indentStk:     []int{0},
		isStartOfLine: true,
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
			lexConsumeComment(l)
			return lexDefault
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

func lexConsumeComment(l *lexer) {
	l.next() // consume '#'

	for {
		r := l.peek()
		if isEndOfLine(r) || r == eof {
			break
		}

		l.next()
	}

	isOnlySpace := true

	for i := l.start - 1; i >= 0; i-- {
		if l.input[i] == '\n' || l.input[i] == '\r' {
			break
		}

		if l.input[i] != ' ' && l.input[i] != '\t' {
			isOnlySpace = false
			break
		}
	}

	if isOnlySpace {
		l.emit(ItemComment)
	} else {
		l.emit(ItemEOLComment)
	}
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

	if (val == CmdCommand || val == CmdFunction) && isSpace(l.peek()) {
		return lexUserCommandOrFunction(l, val)
	}

	if l.peek() == ':' {
		// It's a target, user command, or function.
		l.next() // consume ':'

		l.emit(ItemTarget)
		// Reset indent tracking for the new target
		l.indentStk = []int{0}

		return lexRecipe
	}

	// Fallback to command keyword since it's not a target
	l.pos = l.start

	return lexCommandKeyword
}

func lexUserCommandOrFunction(l *lexer, val string) stateFn {
	l.next()

	for isAlphaNumeric(l.peek()) || l.peek() == '-' || l.peek() == '_' {
		l.next()
	}

	if l.peek() == ':' {
		l.next()
	}

	if val == CmdCommand {
		l.emit(ItemUserCommand)
	} else {
		l.emit(ItemFunction)
	}

	l.indentStk = []int{0}

	return lexRecipe
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

		if nextState := l.checkIndent(indent); nextState != nil {
			return nextState
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
		lexConsumeComment(l)
		return lexRecipe
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

	switch val {
	case "SAVE":
		if strings.HasPrefix(l.input[l.pos:], " ARTIFACT") {
			nextCharPos := l.pos + 9
			if l.isWordBoundary(nextCharPos) {
				l.pos = nextCharPos
				l.col += 9
				val = "SAVE ARTIFACT"
			}
		} else if strings.HasPrefix(l.input[l.pos:], " IMAGE") {
			nextCharPos := l.pos + 6
			if l.isWordBoundary(nextCharPos) {
				l.pos = nextCharPos
				l.col += 6
				val = "SAVE IMAGE"
			}
		}
	case "GIT":
		if strings.HasPrefix(l.input[l.pos:], " CLONE") {
			nextCharPos := l.pos + 6
			if l.isWordBoundary(nextCharPos) {
				l.pos = nextCharPos
				l.col += 6
				val = "GIT CLONE"
			}
		}
	case CmdFrom:
		if strings.HasPrefix(l.input[l.pos:], " DOCKERFILE") {
			nextCharPos := l.pos + 11
			if l.isWordBoundary(nextCharPos) {
				l.pos = nextCharPos
				l.col += 11
				val = CmdFromDockerfile
			}
		}
	case "ELSE":
		if strings.HasPrefix(l.input[l.pos:], " IF") {
			nextCharPos := l.pos + 3
			if l.isWordBoundary(nextCharPos) {
				l.pos = nextCharPos
				l.col += 3
				val = "ELSE IF"
			}
		}
	}

	switch val {
	case CmdFromDockerfile:
		l.emit(ItemFromDockerfile)
	case CmdLocally:
		l.emit(ItemLocally)
	case CmdSaveArtifact:
		l.emit(ItemSaveArtifact)
	case CmdSaveImage:
		l.emit(ItemSaveImage)
	case CmdExpose:
		l.emit(ItemExpose)
	case CmdVolume:
		l.emit(ItemVolume)
	case CmdEnv:
		l.emit(ItemEnv)
	case CmdArg:
		l.emit(ItemArg)
	case CmdSet:
		l.emit(ItemSet)
	case CmdLet:
		l.emit(ItemLet)
	case CmdLabel:
		l.emit(ItemLabel)
	case CmdBuild:
		l.emit(ItemBuild)
	case CmdUser:
		l.emit(ItemUser)
	case CmdCmd:
		l.emit(ItemCmd)
	case CmdEntrypoint:
		l.emit(ItemEntrypoint)
	case CmdGitClone:
		l.emit(ItemGitClone)
	case CmdAdd:
		l.emit(ItemAdd)
	case CmdStopSignal:
		l.emit(ItemStopSignal)
	case CmdOnBuild:
		l.emit(ItemOnBuild)
	case CmdHealthCheck:
		l.emit(ItemHealthCheck)
	case CmdShell:
		l.emit(ItemShell)
	case CmdDo:
		l.emit(ItemDo)
	case CmdCommand:
		l.emit(ItemCommand)
	case CmdFunction:
		l.emit(ItemFunctionKW)
	case CmdImport:
		l.emit(ItemImport)
	case CmdCache:
		l.emit(ItemCache)
	case CmdHost:
		l.emit(ItemHost)
	case CmdProject:
		l.emit(ItemProject)
	case CmdRun:
		l.emit(ItemRun)
	case CmdFrom:
		l.emit(ItemFrom)
	case CmdWorkdir:
		l.emit(ItemWorkdir)
	case CmdCopy:
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
		case r == '#':
			if isFullLineComment(l) {
				l.next() // consume '#'

				for {
					r2 := l.peek()
					if isEndOfLine(r2) || r2 == eof {
						break
					}

					l.next()
				}

				if isEndOfLine(l.peek()) {
					r2 := l.next()
					if r2 == '\r' && l.peek() == '\n' {
						l.next()
					}
				}

				l.ignore()

				continue
			}

			if l.pos > l.start {
				l.emit(ItemAtom)
			}

			isOnlySpace := true

			for i := l.pos - 1; i >= 0; i-- {
				if l.input[i] == '\n' || l.input[i] == '\r' {
					break
				}

				if l.input[i] != ' ' && l.input[i] != '\t' {
					isOnlySpace = false
					break
				}
			}

			lexConsumeComment(l)

			if isOnlySpace {
				r2 := l.peek()
				if isEndOfLine(r2) {
					r2 = l.next()
					if r2 == '\r' && l.peek() == '\n' {
						l.next()
					}
				}
			}

			return lexRecipeCommandArgs
		case r == '\\':
			lexConsumeEscapeOrContinuation(l)
		case r == '"':
			l.next()

			err := lexDoubleQuoteBody(l)
			if err != nil {
				return l.errorf("%v", err)
			}
		case r == '\'':
			l.next()

			err := lexSingleQuoteBody(l)
			if err != nil {
				return l.errorf("%v", err)
			}
		case r == '$':
			l.next()

			if l.peek() == '(' {
				l.next()

				err := lexShellOutBody(l)
				if err != nil {
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
		case r == '#':
			if isFullLineComment(l) {
				l.next() // consume '#'

				for {
					r2 := l.peek()
					if isEndOfLine(r2) || r2 == eof {
						break
					}

					l.next()
				}

				if isEndOfLine(l.peek()) {
					r2 := l.next()
					if r2 == '\r' && l.peek() == '\n' {
						l.next()
					}
				}

				l.ignore()

				continue
			}

			if l.pos > l.start {
				l.emit(ItemAtom)
			}

			isOnlySpace := true

			for i := l.pos - 1; i >= 0; i-- {
				if l.input[i] == '\n' || l.input[i] == '\r' {
					break
				}

				if l.input[i] != ' ' && l.input[i] != '\t' {
					isOnlySpace = false
					break
				}
			}

			lexConsumeComment(l)

			if isOnlySpace {
				r2 := l.peek()
				if isEndOfLine(r2) {
					r2 = l.next()
					if r2 == '\r' && l.peek() == '\n' {
						l.next()
					}
				}
			}

			return lexGlobalCommandArgs
		case r == '\\':
			lexConsumeEscapeOrContinuation(l)
		case r == '"':
			l.next()

			err := lexDoubleQuoteBody(l)
			if err != nil {
				return l.errorf("%v", err)
			}
		case r == '\'':
			l.next()

			err := lexSingleQuoteBody(l)
			if err != nil {
				return l.errorf("%v", err)
			}
		case r == '$':
			l.next()

			if l.peek() == '(' {
				l.next()

				err := lexShellOutBody(l)
				if err != nil {
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
	r := l.next()
	if r == '\r' && l.peek() == '\n' {
		l.next() // consume \n
	}
}

func isFullLineComment(l *lexer) bool {
	if l.peek() != '#' {
		return false
	}

	for i := int(l.pos) - 1; i >= 0; i-- {
		c := l.input[i]
		if c == '\n' || c == '\r' {
			return true
		}

		if c != ' ' && c != '\t' {
			return false
		}
	}

	return true
}

func lexConsumeEscapeOrContinuation(l *lexer) {
	idx := l.pos + 1
	isContinuation := false

	for idx < Pos(len(l.input)) {
		r, w := utf8.DecodeRuneInString(l.input[idx:])
		if r == ' ' || r == '\t' {
			idx += Pos(w)
			continue
		}

		if r == '#' {
			isContinuation = true
			break
		}

		if r == '\r' || r == '\n' || r == eof {
			isContinuation = true
			break
		}

		break
	}

	if idx >= Pos(len(l.input)) {
		isContinuation = true
	}

	if isContinuation {
		lexConsumeContinuation(l, l.pos == l.start)
	} else {
		l.next() // consume \

		r := l.next()
		if r == '\r' && l.peek() == '\n' {
			l.next()
		}
	}
}

func lexConsumeContinuation(l *lexer, shouldIgnore bool) {
	l.next() // consume \

	for {
		r2 := l.peek()
		if r2 == eof {
			break
		}

		if r2 == ' ' || r2 == '\t' {
			l.next()
			continue
		}

		if r2 == '#' {
			l.next() // consume '#'

			for {
				r3 := l.peek()
				if isEndOfLine(r3) || r3 == eof {
					break
				}

				l.next()
			}

			continue
		}

		if isEndOfLine(r2) {
			r3 := l.next()
			if r3 == '\r' && l.peek() == '\n' {
				l.next()
			}

			continue // continue loop to consume spaces/comments on next line
		}

		break
	}

	if shouldIgnore {
		l.ignore()
	}
}

func lexDoubleQuoteBody(l *lexer) error {
	for {
		r := l.next()
		switch r {
		case eof:
			return errors.New("unclosed double quote")
		case '"':
			return nil // done
		case '\\':
			lexConsumeEscape(l) // consume escaped char
		case '$':
			if l.peek() == '(' {
				l.next() // consume '('

				err := lexShellOutBody(l)
				if err != nil {
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
			return errors.New("unclosed single quote")
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
			return errors.New("unclosed shell substitution")
		case '"':
			err := lexDoubleQuoteBody(l)
			if err != nil {
				return err
			}
		case '\'':
			err := lexSingleQuoteBody(l)
			if err != nil {
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

func (l *lexer) isWordBoundary(pos Pos) bool {
	if int(pos) >= len(l.input) {
		return true
	}

	r := rune(l.input[pos])

	return isSpace(r) || isEndOfLine(r)
}

func (l *lexer) peekNextNonCommentIndent() int {
	idx := l.pos
	// Skip the current comment line
	for idx < Pos(len(l.input)) {
		c := l.input[idx]
		if c == '\n' || c == '\r' {
			idx++
			if c == '\r' && idx < Pos(len(l.input)) && l.input[idx] == '\n' {
				idx++
			}

			break
		}

		idx++
	}

	// Now scan lines until we find a non-comment, non-empty line
	for idx < Pos(len(l.input)) {
		isComment := false
		isEmpty := true
		indent := 0

		// Scan the line
		for idx < Pos(len(l.input)) {
			c := l.input[idx]
			if c == '\n' || c == '\r' {
				idx++
				if c == '\r' && idx < Pos(len(l.input)) && l.input[idx] == '\n' {
					idx++
				}

				break
			}

			switch c {
			case ' ', '\t':
				if isEmpty {
					indent++
				}

			case '#':
				isComment = true
				isEmpty = false

			default:
				isEmpty = false
			}

			idx++
		}

		if !isComment && !isEmpty {
			return indent
		}
	}

	return 0
}

func (l *lexer) checkIndent(indent int) stateFn {
	if l.peek() != '#' {
		if !isEndOfLine(l.peek()) && l.peek() != eof {
			isIndented := indent > 0
			wasIndented := len(l.indentStk) > 1

			switch {
			case isIndented && !wasIndented:
				l.indentStk = append(l.indentStk, 1)
				l.emit(ItemIndent)

			case !isIndented && wasIndented:
				l.indentStk = l.indentStk[:1]
				l.emit(ItemDedent)

				return lexDefault

			case !isIndented && !wasIndented:
				return lexDefault
			}
		}

		return nil
	}

	if indent != 0 {
		l.ignore()

		return nil
	}

	nextIndent := l.peekNextNonCommentIndent()
	if nextIndent != 0 {
		l.ignore()

		return nil
	}

	wasIndented := len(l.indentStk) > 1
	if wasIndented {
		l.indentStk = l.indentStk[:1]
		l.emit(ItemDedent)

		return lexDefault
	}

	return nil
}
