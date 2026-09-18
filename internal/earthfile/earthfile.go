// Package earthfile defines the core Earthfile AST structure and provides parsing entry points.
package earthfile

import (
	"fmt"
	"slices"
)

// TargetBase is the name of the default target which is used when an
// Earthfile is parsed which does not have any targets.
const TargetBase = "base"

// Tree is the AST representation of an Earthfile.
type Tree struct {
	Version        *Version       `json:"version,omitempty"`
	Targets        []Target       `json:"targets,omitempty"`
	Functions      []Function     `json:"functions,omitempty"`
	BaseRecipe     Block          `json:"baseRecipe,omitempty"`
	SourceLocation SourceLocation `json:"sourceLocation,omitzero"`
}

// Target is the AST representation of an Earthfile target.
type Target struct {
	Name           string         `json:"name"`
	Docs           string         `json:"docs,omitempty"`
	Recipe         Block          `json:"recipe,omitempty"`
	SourceLocation SourceLocation `json:"sourceLocation,omitzero"`
}

// Function is the AST representation of an Earthfile function definition.
type Function struct {
	Name           string         `json:"name"`
	Recipe         Block          `json:"recipe,omitempty"`
	SourceLocation SourceLocation `json:"sourceLocation,omitzero"`
}

// Version is the AST representation of an Earthfile version definition.
type Version struct {
	Args           []string       `json:"args"`
	SourceLocation SourceLocation `json:"sourceLocation,omitzero"`
}

// Block is the AST representation of a block of statements.
type Block []Statement

// Statement is the AST representation of an Earthfile statement. Only one field may be
// filled at one time.
type Statement struct {
	Command *Command       `json:"command,omitempty"`
	With    *WithStatement `json:"with,omitempty"`
	If      *IfStatement   `json:"if,omitempty"`
	Try     *TryStatement  `json:"try,omitempty"`
	For     *ForStatement  `json:"for,omitempty"`
	Wait    *WaitStatement `json:"wait,omitempty"`
}

// Location returns the source location of the inner statement.
func (s Statement) Location() SourceLocation {
	switch {
	case s.Command != nil:
		return s.Command.SourceLocation
	case s.With != nil:
		return s.With.SourceLocation
	case s.If != nil:
		return s.If.SourceLocation
	case s.Try != nil:
		return s.Try.SourceLocation
	case s.For != nil:
		return s.For.SourceLocation
	case s.Wait != nil:
		return s.Wait.SourceLocation
	default:
		return SourceLocation{}
	}
}

// Command is the AST representation of an Earthfile command.
type Command struct {
	Name           Cmd            `json:"name"`
	Docs           string         `json:"docs,omitempty"`
	Args           []string       `json:"args,omitempty"`
	SourceLocation SourceLocation `json:"sourceLocation,omitzero"`
	ExecMode       bool           `json:"execMode,omitzero"`
}

// Clone returns a deep copy of the command.
func (c Command) Clone() Command {
	newCmd := c
	newCmd.Args = slices.Clone(c.Args)

	return newCmd
}

// WithStatement is the AST representation of a "WITH" statement.
type WithStatement struct {
	Body           Block          `json:"body,omitempty"`
	SourceLocation SourceLocation `json:"sourceLocation,omitzero"`
	Command        Command        `json:"command"`
}

// IfStatement is the AST representation of an "IF" statement.
type IfStatement struct {
	ElseBody       *Block            `json:"elseBody,omitempty"`
	Expression     []string          `json:"expression"`
	ElseIf         []ElseIfStatement `json:"elseIf,omitempty"`
	IfBody         Block             `json:"ifBody,omitempty"`
	SourceLocation SourceLocation    `json:"sourceLocation,omitzero"`
	ExecMode       bool              `json:"execMode,omitzero"`
}

// TryStatement is the AST representation of a "TRY" statement.
type TryStatement struct {
	CatchBody      *Block         `json:"catchBody,omitempty"`
	FinallyBody    *Block         `json:"finallyBody,omitempty"`
	TryBody        Block          `json:"tryBody,omitempty"`
	SourceLocation SourceLocation `json:"sourceLocation,omitzero"`
}

// ElseIfStatement is the AST representation of an "ELSE IF" clause.
type ElseIfStatement struct {
	Expression     []string       `json:"expression"`
	Body           Block          `json:"body,omitempty"`
	SourceLocation SourceLocation `json:"sourceLocation,omitzero"`
	ExecMode       bool           `json:"execMode,omitzero"`
}

// ForStatement is the AST representation of a "FOR" statement.
type ForStatement struct {
	Args           []string       `json:"args"`
	Body           Block          `json:"body,omitempty"`
	SourceLocation SourceLocation `json:"sourceLocation,omitzero"`
}

// WaitStatement is the AST representation of a "WAIT" statement.
type WaitStatement struct {
	Args           []string       `json:"args"`
	Body           Block          `json:"body,omitempty"`
	SourceLocation SourceLocation `json:"sourceLocation,omitzero"`
}

// SourceLocation represents a position in an Earthfile source file.
type SourceLocation struct {
	File        string `json:"file,omitempty"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	EndLine     int    `json:"endLine"`
	EndColumn   int    `json:"endColumn"`
}

// IsZero reports whether the source location is unpopulated.
func (sl SourceLocation) IsZero() bool {
	return sl.File == "" || sl.StartLine <= 0
}

// String returns the standard "file:line:column" representation of the source location.
func (sl SourceLocation) String() string {
	if sl.IsZero() {
		return ""
	}

	if sl.StartColumn <= 0 {
		return fmt.Sprintf("%s:%d", sl.File, sl.StartLine)
	}

	return fmt.Sprintf("%s:%d:%d", sl.File, sl.StartLine, sl.StartColumn)
}

// Error represents a syntax or validation error in an Earthfile.
type Error struct {
	// Msg is the error description.
	Msg string
	// Location is the position in the source file where the error occurred.
	Location SourceLocation
}

// Error implements the error interface, formatting the location and message.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	loc := e.Location.String()
	if loc == "" {
		return e.Msg
	}

	if e.Msg == "" {
		return loc
	}

	return loc + ": " + e.Msg
}
