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
	CmdCatch          = "CATCH"
	CmdCmd            = "CMD"
	CmdCommand        = "COMMAND"
	CmdCopy           = "COPY"
	CmdDo             = "DO"
	CmdDocker         = "DOCKER"
	CmdElse           = "ELSE"
	CmdElseIf         = "ELSE IF"
	CmdEnd            = "END"
	CmdEntrypoint     = "ENTRYPOINT"
	CmdEnv            = "ENV"
	CmdExpose         = "EXPOSE"
	CmdFinally        = "FINALLY"
	CmdFor            = "FOR"
	CmdFrom           = "FROM"
	CmdFromDockerfile = "FROM DOCKERFILE"
	CmdFunction       = "FUNCTION"
	CmdGitClone       = "GIT CLONE"
	CmdHealthCheck    = "HEALTHCHECK"
	CmdHost           = "HOST"
	CmdIf             = "IF"
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
	CmdTry            = "TRY"
	CmdUser           = "USER"
	CmdVersion        = "VERSION"
	CmdVolume         = "VOLUME"
	CmdWait           = "WAIT"
	CmdWith           = "WITH"
	CmdWorkdir        = "WORKDIR"
)

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
	switch i.Typ {
	case ItemEOF:
		return "EOF"
	case ItemError:
		return i.Val
	case ItemNL, ItemIndent, ItemDedent, ItemWS, ItemComment, ItemEOLComment,
		ItemFrom, ItemFromDockerfile, ItemLocally, ItemCopy, ItemSaveArtifact,
		ItemSaveImage, ItemRun, ItemExpose, ItemVolume, ItemEnv, ItemArg,
		ItemSet, ItemLet, ItemLabel, ItemBuild, ItemWorkdir, ItemIf,
		ItemElseIf, ItemElse, ItemEnd, ItemCmd, ItemEntrypoint, ItemGitClone,
		ItemAdd, ItemStopSignal, ItemOnBuild, ItemHealthCheck, ItemShell,
		ItemDo, ItemCommand, ItemFunctionKW, ItemImport, ItemVersion, ItemCache,
		ItemHost, ItemProject, ItemUser, ItemWith, ItemDocker, ItemTry,
		ItemCatch, ItemFinally, ItemFor, ItemWait, ItemTarget, ItemUserCommand,
		ItemFunction, ItemAtom, ItemEquals:
		return fmt.Sprintf("%q", i.Val)
	}

	return fmt.Sprintf("%q", i.Val)
}

// stateFn represents the state of the scanner as a function that returns the next state.
type stateFn func(*lexer) stateFn

// lexer holds the state of the scanner.
type lexer struct {
	input           string
	name            string
	keyValueCmdType string
	state           stateFn
	itemsArr        [32]Item
	indentArr       [16]int
	itemsStart      int
	itemsEnd        int
	indentLen       int
	line            int
	lastPos         Pos
	width           Pos
	start           Pos
	col             int
	startLine       int
	startCol        int
	pos             Pos
	isStartOfLine   bool
}

// next returns the next rune in the input.
func (l *lexer) next() rune {
	if int(l.pos) >= len(l.input) {
		l.width = 0
		return eof
	}

	c := l.input[l.pos]
	if c < utf8.RuneSelf {
		l.width = 1
		l.pos++

		if c == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}

		return rune(c)
	}

	return l.nextUnicode()
}

func (l *lexer) nextUnicode() rune {
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.width = Pos(w)
	l.pos += l.width

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
	if int(l.pos) >= len(l.input) {
		return eof
	}

	c := l.input[l.pos]
	if c < utf8.RuneSelf {
		return rune(c)
	}

	r, _ := utf8.DecodeRuneInString(l.input[l.pos:])

	return r
}

// emit passes an item back to the client.
func (l *lexer) emit(t ItemType) {
	if l.itemsEnd < len(l.itemsArr) {
		l.itemsArr[l.itemsEnd] = Item{
			Typ:  t,
			Pos:  l.start,
			Val:  l.input[l.start:l.pos],
			Line: l.startLine,
			Col:  l.startCol,
		}
		l.itemsEnd++
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
	if l.itemsEnd < len(l.itemsArr) {
		l.itemsArr[l.itemsEnd] = Item{
			Typ:  ItemError,
			Pos:  l.start,
			Val:  fmt.Sprintf(format, args...),
			Line: l.startLine,
			Col:  l.startCol,
		}
		l.itemsEnd++
	}

	return nil
}

// nextItem returns the next item from the input.
func (l *lexer) nextItem() Item {
	for l.itemsStart == l.itemsEnd && l.state != nil {
		l.itemsStart = 0
		l.itemsEnd = 0
		l.state = l.state(l)
	}

	if l.itemsStart < l.itemsEnd {
		item := l.itemsArr[l.itemsStart]
		l.itemsStart++
		l.lastPos = item.Pos

		return item
	}

	return Item{
		Typ:  ItemEOF,
		Pos:  l.pos,
		Line: l.line,
		Col:  l.col,
	}
}

// lex creates a new scanner for the input string.
func lex(name, input string) *lexer {
	l := &lexer{
		name:          name,
		input:         input,
		line:          1,
		col:           1,
		startLine:     1,
		startCol:      1,
		isStartOfLine: true,
		state:         lexDefault,
		indentLen:     1,
	}

	return l
}

// isSpace reports whether r is a space character.
func isSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

// skipSpace consumes consecutive space and tab characters.
func (l *lexer) skipSpace() {
	for int(l.pos) < len(l.input) {
		c := l.input[l.pos]
		if c != ' ' && c != '\t' {
			break
		}

		l.pos++
		l.col++
	}
}

// skipSpaceCount consumes consecutive space and tab characters, returning the number of characters consumed.
func (l *lexer) skipSpaceCount() int {
	start := l.pos
	l.skipSpace()

	return int(l.pos - start)
}

// isEndOfLine reports whether r is an end-of-line character.
func isEndOfLine(r rune) bool {
	return r == '\r' || r == '\n'
}

// isAlphaNumeric reports whether r is an alphabetic, digit, or underscore.
func isAlphaNumeric(r rune) bool {
	return ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') || r == '_'
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

// lexSpace consumes consecutive space and tab characters and emits an ItemWS token.
func lexSpace(l *lexer) stateFn {
	l.skipSpace()

	l.emit(ItemWS)

	return lexDefault
}

// lexNL consumes a newline sequence (including Windows-style CRLF) and emits an ItemNL token.
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

// lexConsumeComment consumes characters until the end of the line
// and emits either ItemComment (if the comment starts the line) or ItemEOLComment.
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

	typ := ItemEOLComment
	if isOnlySpace {
		typ = ItemComment
	}

	l.emit(typ)
}

// lexIdentifier parses target names, custom user commands, functions, or version keywords.
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
	switch val {
	case CmdVersion:
		l.emit(ItemVersion)

		return lexGlobalCommandArgs
	case CmdProject:
		l.emit(ItemProject)

		return lexGlobalCommandArgs
	case CmdCommand, CmdFunction:
		if isSpace(l.peek()) {
			return lexUserCommandOrFunction(l, val)
		}
	}

	if l.peek() == ':' {
		// It's a target, user command, or function.
		l.next() // consume ':'

		l.emit(ItemTarget)
		// Reset indent tracking for the new target
		l.indentArr[0] = 0
		l.indentLen = 1

		return lexRecipe
	}

	// Fallback to command keyword since it's not a target
	l.pos = l.start
	l.col = l.startCol
	l.line = l.startLine

	return lexCommandKeyword
}

// lexUserCommandOrFunction parses the name of a custom user command or function definition.
func lexUserCommandOrFunction(l *lexer, val string) stateFn {
	l.next()

	for isAlphaNumeric(l.peek()) || l.peek() == '-' || l.peek() == '_' {
		l.next()
	}

	if l.peek() == ':' {
		l.next()
	}

	typ := ItemFunction
	if val == CmdCommand {
		typ = ItemUserCommand
	}

	l.emit(typ)

	l.indentArr[0] = 0
	l.indentLen = 1

	return lexRecipe
}

// lexRecipe processes tokens inside target recipes, handling indentation levels and keywords.
func lexRecipe(l *lexer) stateFn {
	// First, check for indentation changes at the start of a line
	if l.col == 1 {
		indent := l.skipSpaceCount()

		if l.pos > l.start {
			l.ignore() // we don't emit WS for indentation, we emit ItemIndent/Dedent
		}

		nextState := l.checkIndent(indent)
		if nextState != nil {
			return nextState
		}
	}

	r := l.peek()
	switch {
	case r == eof:
		// Dedent everything
		for l.indentLen > 1 {
			l.indentLen--
			l.emit(ItemDedent)
		}

		l.emit(ItemEOF)

		return nil
	case isSpace(r):
		l.skipSpace()
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

// lexCommandKeyword consumes characters of a command keyword (like RUN or FROM) and emits the matching ItemType.
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

	var (
		typ       ItemType
		nextState = lexRecipeCommandArgs
	)

	switch val {
	case "SAVE":
		switch {
		case l.isWordBoundary(l.pos+9) && strings.HasPrefix(l.input[l.pos:], " ARTIFACT"):
			l.pos += 9
			l.col += 9
			typ = ItemSaveArtifact
		case l.isWordBoundary(l.pos+6) && strings.HasPrefix(l.input[l.pos:], " IMAGE"):
			l.pos += 6
			l.col += 6
			typ = ItemSaveImage
		default:
			return l.errorf("unknown command keyword: %q", val)
		}
	case "GIT":
		if l.isWordBoundary(l.pos+6) && strings.HasPrefix(l.input[l.pos:], " CLONE") {
			l.pos += 6
			l.col += 6
			typ = ItemGitClone
		} else {
			return l.errorf("unknown command keyword: %q", val)
		}
	case CmdFrom:
		if l.isWordBoundary(l.pos+11) && strings.HasPrefix(l.input[l.pos:], " DOCKERFILE") {
			l.pos += 11
			l.col += 11
			typ = ItemFromDockerfile
		} else {
			typ = ItemFrom
		}
	case CmdElse:
		if l.isWordBoundary(l.pos+3) && strings.HasPrefix(l.input[l.pos:], " IF") {
			l.pos += 3
			l.col += 3
			typ = ItemElseIf
		} else {
			typ = ItemElse
		}
	case CmdLocally:
		typ = ItemLocally
	case CmdExpose:
		typ = ItemExpose
	case CmdVolume:
		typ = ItemVolume
	case CmdEnv:
		typ = ItemEnv
		l.keyValueCmdType = CmdEnv
		nextState = lexKeyValueCommandArgs
	case CmdArg:
		typ = ItemArg
		l.keyValueCmdType = CmdArg
		nextState = lexKeyValueCommandArgs
	case CmdSet:
		typ = ItemSet
		l.keyValueCmdType = CmdSet
		nextState = lexKeyValueCommandArgs
	case CmdLet:
		typ = ItemLet
		l.keyValueCmdType = CmdLet
		nextState = lexKeyValueCommandArgs
	case CmdLabel:
		typ = ItemLabel
	case CmdBuild:
		typ = ItemBuild
	case CmdUser:
		typ = ItemUser
	case CmdCmd:
		typ = ItemCmd
	case CmdEntrypoint:
		typ = ItemEntrypoint
	case CmdAdd:
		typ = ItemAdd
	case CmdStopSignal:
		typ = ItemStopSignal
	case CmdOnBuild:
		typ = ItemOnBuild
	case CmdHealthCheck:
		typ = ItemHealthCheck
	case CmdShell:
		typ = ItemShell
	case CmdDo:
		typ = ItemDo
	case CmdCommand:
		typ = ItemCommand
	case CmdFunction:
		typ = ItemFunctionKW
	case CmdImport:
		typ = ItemImport
	case CmdCache:
		typ = ItemCache
	case CmdHost:
		typ = ItemHost
	case CmdProject:
		typ = ItemProject
	case CmdRun:
		typ = ItemRun
	case CmdWorkdir:
		typ = ItemWorkdir
	case CmdCopy:
		typ = ItemCopy
	case CmdIf:
		typ = ItemIf
	case CmdEnd:
		typ = ItemEnd
	case CmdFor:
		typ = ItemFor
	case CmdTry:
		typ = ItemTry
	case CmdCatch:
		typ = ItemCatch
	case CmdFinally:
		typ = ItemFinally
	case CmdWith:
		typ = ItemWith
	case CmdWait:
		typ = ItemWait
	default:
		return l.errorf("unknown command keyword: %q", val)
	}

	l.emit(typ)

	return nextState
}

// lexRecipeCommandArgs parses command arguments within a recipe, processing strings, shell outs, and continuations.
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
			if !isCommentStart(l) {
				l.next()

				continue
			}

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

// lexRecipeSpaceArgs consumes whitespace within a recipe command's arguments list.
func lexRecipeSpaceArgs(l *lexer) stateFn {
	l.skipSpace()

	l.emit(ItemWS)

	return lexRecipeCommandArgs
}

// lexKeyValueCommandArgs parses flag, key, and equals tokens for commands like ENV, ARG, SET, or LET.
func lexKeyValueCommandArgs(l *lexer) stateFn {
	// 1. Skip leading space
	l.skipSpace()

	if l.pos > l.start {
		l.emit(ItemWS)
	}

	// 2. Check for flags (atoms starting with '-')
	if l.peek() == '-' {
		// Lex flag as ItemAtom
		for {
			r := l.peek()
			if isSpace(r) || isEndOfLine(r) || r == eof || r == '#' || r == '=' {
				break
			}

			l.next()
		}

		l.emit(ItemAtom)

		// After the flag, continue in lexKeyValueCommandArgs to expect more flags or the key
		return lexKeyValueCommandArgs
	}

	// 3. We are now expecting the key (the env variable name)
	r := l.peek()
	if r == eof || isEndOfLine(r) || r == '#' {
		// Empty key or comment/newline, let standard lexer handle EOF/newline/comment
		return lexRecipeCommandArgs
	}

	// Ensure the first character is valid for variable name
	if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '_' {
		// Consuming the rest of the invalid identifier for a better error message
		for {
			nextR := l.peek()
			if isSpace(nextR) || nextR == '=' || isEndOfLine(nextR) || nextR == eof || nextR == '#' {
				break
			}

			l.next()
		}

		return l.errorf("invalid %s key definition %s", l.keyValueCmdType, l.input[l.start:l.pos])
	}

	l.next() // consume first char

	// Read subsequent valid characters
	for {
		r = l.peek()
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			l.next()
		} else {
			break
		}
	}

	// The key is complete. Let's inspect the next character to make sure it is a valid boundary.
	// Valid boundaries after a key: space, '=', newline, comment, EOF.
	nextR := l.peek()
	if !isSpace(nextR) && nextR != '=' && !isEndOfLine(nextR) && nextR != eof && nextR != '#' {
		// Consume the rest of the invalid identifier for error message
		for {
			nextR = l.peek()
			if isSpace(nextR) || nextR == '=' || isEndOfLine(nextR) || nextR == eof || nextR == '#' {
				break
			}

			l.next()
		}

		return l.errorf("invalid %s key definition %s", l.keyValueCmdType, l.input[l.start:l.pos])
	}

	// Key is valid! Emit it.
	l.emit(ItemAtom)

	// If the next character is '=', consume it and emit as ItemAtom
	if l.peek() == '=' {
		l.next()
		l.emit(ItemAtom)
	}

	// Now that we have lexed the key and optional '=', transition to the standard lexRecipeCommandArgs for values
	return lexRecipeCommandArgs
}

// lexGlobalCommandArgs parses arguments for global scope commands such as VERSION or PROJECT.
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
			if !isCommentStart(l) {
				l.next()

				continue
			}

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

// lexGlobalSpaceArgs consumes whitespace within global command arguments lists.
func lexGlobalSpaceArgs(l *lexer) stateFn {
	l.skipSpace()
	l.emit(ItemWS)

	return lexGlobalCommandArgs
}

// lexConsumeEscape consumes an escaped character, including windows-style line endings.
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

func isCommentStart(l *lexer) bool {
	if l.peek() != '#' {
		return false
	}

	if l.pos == 0 {
		return true
	}

	prev := l.input[l.pos-1]

	return prev == ' ' || prev == '\t' || prev == '\n' || prev == '\r'
}

// lexConsumeEscapeOrContinuation determines whether a backslash indicates
// a line continuation or an escaped character, and consumes it accordingly.
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

// lexConsumeContinuation consumes a backslash line continuation,
// optionally ignoring the whitespace and comments that follow.
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

// lexDoubleQuoteBody parses the contents inside double quotes, including nested shell substitutions.
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

// lexSingleQuoteBody parses the contents inside single quotes.
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

// lexShellOutBody parses the contents of a $(...) shell execution body, balancing parentheses and nested strings.
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

// checkIndent evaluates changes in indentation for target recipes and emits indent/dedent tokens.
// It returns a stateFn if a state transition is required (e.g., returning lexDefault when returning
// to 0 indentation), or nil if lexing should continue in the current recipe context.
//
// For non-comment lines:
// - Transition from unindented to indented emits ItemIndent.
// - Transition from indented to unindented emits ItemDedent and returns lexDefault.
// - Unindented lines without previous indentation transition back to lexDefault.
//
// For comment lines starting with '#':
//   - If the comment is indented, it is ignored without emitting indent tokens.
//   - If the comment is at 0 indentation but the next non-comment line is indented, the 0-indent
//     comment is ignored to avoid premature dedenting.
//   - If both the comment and the next non-comment line are at 0 indentation, it dedents (if
//     previously indented) and returns lexDefault.
func (l *lexer) checkIndent(indent int) stateFn {
	if l.peek() != '#' {
		if !isEndOfLine(l.peek()) && l.peek() != eof {
			isIndented := indent > 0
			wasIndented := l.indentLen > 1

			switch {
			case isIndented && !wasIndented:
				if l.indentLen < len(l.indentArr) {
					l.indentArr[l.indentLen] = 1
					l.indentLen++
				}

				l.emit(ItemIndent)

			case !isIndented && wasIndented:
				l.indentLen = 1
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

	wasIndented := l.indentLen > 1
	if wasIndented {
		l.indentLen = 1
		l.emit(ItemDedent)

		return lexDefault
	}

	return nil
}
