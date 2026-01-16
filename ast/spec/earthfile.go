package spec

// Earthfile is the AST representation of an Earthfile.
type Earthfile struct {
	Version        *Version        `json:"version,omitempty"`
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Targets        []Target        `json:"targets,omitempty"`
	Functions      []Function      `json:"functions,omitempty"`
	BaseRecipe     Block           `json:"baseRecipe"`
}

// Target is the AST representation of an Earthfile target.
type Target struct {
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Name           string          `json:"name"`
	Docs           string          `json:"docs,omitempty"`
	Recipe         Block           `json:"recipe"`
}

// Function is the AST representation of an Earthfile function definition.
type Function struct {
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Name           string          `json:"name"`
	Recipe         Block           `json:"recipe"`
}

// Version is the AST representation of an Earthfile version definition.
type Version struct {
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Args           []string        `json:"args"`
}

// Block is the AST representation of a block of statements.
type Block []Statement

// Statement is the AST representation of an Earthfile statement. Only one field may be
// filled at one time.
type Statement struct {
	Command        *Command        `json:"command,omitempty"`
	With           *WithStatement  `json:"with,omitempty"`
	If             *IfStatement    `json:"if,omitempty"`
	Try            *TryStatement   `json:"try,omitempty"`
	For            *ForStatement   `json:"for,omitempty"`
	Wait           *WaitStatement  `json:"wait,omitempty"`
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
}

// Command is the AST representation of an Earthfile command.
type Command struct {
	Name           string          `json:"name"`
	Docs           string          `json:"docs,omitempty"`
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Args           []string        `json:"args"`
	ExecMode       bool            `json:"execMode,omitempty"`
}

func (c Command) Clone() Command {
	newCmd := c
	args := make([]string, len(c.Args))
	copy(args, c.Args)
	newCmd.Args = args
	srcLoc := *c.SourceLocation
	newCmd.SourceLocation = &srcLoc

	return newCmd
}

// WithStatement is the AST representation of a with statement.
type WithStatement struct {
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Body           Block           `json:"body"`
	Command        Command         `json:"command"`
}

// IfStatement is the AST representation of an if statement.
type IfStatement struct {
	ElseBody       *Block          `json:"elseBody,omitempty"`
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Expression     []string        `json:"expression"`
	ElseIf         []ElseIf        `json:"elseIf,omitempty"`
	IfBody         Block           `json:"ifBody"`
	ExecMode       bool            `json:"execMode,omitempty"`
}

// TryStatement is the AST representation of a try statement.
type TryStatement struct {
	CatchBody      *Block          `json:"catchBody,omitempty"`
	FinallyBody    *Block          `json:"finallyBody,omitempty"`
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	TryBody        Block           `json:"tryBody"`
}

// ElseIf is the AST representation of an else if clause.
type ElseIf struct {
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Expression     []string        `json:"expression"`
	Body           Block           `json:"body"`
	ExecMode       bool            `json:"execMode,omitempty"`
}

// ForStatement is the AST representation of a for statement.
type ForStatement struct {
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Args           []string        `json:"args"`
	Body           Block           `json:"body"`
}

// WaitStatement is the AST representation of a for statement.
type WaitStatement struct {
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Args           []string        `json:"args"`
	Body           Block           `json:"body"`
}

// SourceLocation is an optional reference to the original source code location.
type SourceLocation struct {
	File        string `json:"file,omitempty"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	EndLine     int    `json:"endLine"`
	EndColumn   int    `json:"endColumn"`
}
