// Package earthfile defines the core Earthfile AST structure and provides parsing entry points.
package earthfile

import "github.com/EarthBuild/earthbuild/internal/earthfile/parse"

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

// Clone returns a deep copy of the command.
func (c Command) Clone() Command {
	newCmd := c
	args := make([]string, len(c.Args))
	copy(args, c.Args)
	newCmd.Args = args

	if c.SourceLocation != nil {
		srcLoc := *c.SourceLocation
		newCmd.SourceLocation = &srcLoc
	}

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

// Parse parses the Earthfile text and returns an Earthfile.
func Parse(name, text string) (Earthfile, error) {
	tree, err := parse.Parse(name, text)
	if err != nil {
		return Earthfile{}, err
	}

	return mapTree(tree), nil
}

func mapArgs(args []string) []string {
	if args == nil {
		return []string{}
	}

	return args
}

func mapSourceLocation(src *parse.SourceLocation) *SourceLocation {
	if src == nil {
		return nil
	}

	return &SourceLocation{
		File:        src.File,
		StartLine:   src.StartLine,
		StartColumn: src.StartColumn,
		EndLine:     src.EndLine,
		EndColumn:   src.EndColumn,
	}
}

func mapVersion(v *parse.Version) *Version {
	if v == nil {
		return nil
	}

	return &Version{
		SourceLocation: mapSourceLocation(v.SourceLocation),
		Args:           mapArgs(v.Args),
	}
}

func mapBlock(b parse.Block) Block {
	if b == nil {
		return nil
	}

	res := make(Block, len(b))

	for i, stmt := range b {
		res[i] = mapStatement(stmt)
	}

	return res
}

func mapStatement(s parse.Statement) Statement {
	return Statement{
		Command:        mapCommand(s.Command),
		With:           mapWithStatement(s.With),
		If:             mapIfStatement(s.If),
		Try:            mapTryStatement(s.Try),
		For:            mapForStatement(s.For),
		Wait:           mapWaitStatement(s.Wait),
		SourceLocation: mapSourceLocation(s.SourceLocation),
	}
}

func mapCommand(c *parse.Command) *Command {
	if c == nil {
		return nil
	}

	return &Command{
		Name:           c.Name,
		Docs:           c.Docs,
		SourceLocation: mapSourceLocation(c.SourceLocation),
		Args:           mapArgs(c.Args),
		ExecMode:       c.ExecMode,
	}
}

func mapWithStatement(w *parse.WithStatement) *WithStatement {
	if w == nil {
		return nil
	}

	var cmd Command

	if w.Command.SourceLocation != nil {
		cmd = *mapCommand(&w.Command)
	} else {
		cmd = Command{
			Name:     w.Command.Name,
			Docs:     w.Command.Docs,
			Args:     mapArgs(w.Command.Args),
			ExecMode: w.Command.ExecMode,
		}
	}

	return &WithStatement{
		SourceLocation: mapSourceLocation(w.SourceLocation),
		Body:           mapBlock(w.Body),
		Command:        cmd,
	}
}

func mapIfStatement(f *parse.IfStatement) *IfStatement {
	if f == nil {
		return nil
	}

	var elseBody *Block

	if f.ElseBody != nil {
		eb := mapBlock(*f.ElseBody)
		elseBody = &eb
	}

	var elseIfs []ElseIf

	if f.ElseIf != nil {
		elseIfs = make([]ElseIf, len(f.ElseIf))

		for i, ei := range f.ElseIf {
			elseIfs[i] = ElseIf{
				SourceLocation: mapSourceLocation(ei.SourceLocation),
				Expression:     mapArgs(ei.Expression),
				Body:           mapBlock(ei.Body),
				ExecMode:       ei.ExecMode,
			}
		}
	}

	return &IfStatement{
		ElseBody:       elseBody,
		SourceLocation: mapSourceLocation(f.SourceLocation),
		Expression:     mapArgs(f.Expression),
		ElseIf:         elseIfs,
		IfBody:         mapBlock(f.IfBody),
		ExecMode:       f.ExecMode,
	}
}

func mapTryStatement(t *parse.TryStatement) *TryStatement {
	if t == nil {
		return nil
	}

	var catchBody *Block

	if t.CatchBody != nil {
		cb := mapBlock(*t.CatchBody)
		catchBody = &cb
	}

	var finallyBody *Block

	if t.FinallyBody != nil {
		fb := mapBlock(*t.FinallyBody)
		finallyBody = &fb
	}

	return &TryStatement{
		CatchBody:      catchBody,
		FinallyBody:    finallyBody,
		SourceLocation: mapSourceLocation(t.SourceLocation),
		TryBody:        mapBlock(t.TryBody),
	}
}

func mapForStatement(f *parse.ForStatement) *ForStatement {
	if f == nil {
		return nil
	}

	return &ForStatement{
		SourceLocation: mapSourceLocation(f.SourceLocation),
		Args:           mapArgs(f.Args),
		Body:           mapBlock(f.Body),
	}
}

func mapWaitStatement(w *parse.WaitStatement) *WaitStatement {
	if w == nil {
		return nil
	}

	return &WaitStatement{
		SourceLocation: mapSourceLocation(w.SourceLocation),
		Args:           mapArgs(w.Args),
		Body:           mapBlock(w.Body),
	}
}

func mapTree(tree parse.Tree) Earthfile {
	var targets []Target

	if tree.Targets != nil {
		targets = make([]Target, len(tree.Targets))

		for i, t := range tree.Targets {
			targets[i] = Target{
				SourceLocation: mapSourceLocation(t.SourceLocation),
				Name:           t.Name,
				Docs:           t.Docs,
				Recipe:         mapBlock(t.Recipe),
			}
		}
	}

	var functions []Function

	if tree.Functions != nil {
		functions = make([]Function, len(tree.Functions))

		for i, fn := range tree.Functions {
			functions[i] = Function{
				SourceLocation: mapSourceLocation(fn.SourceLocation),
				Name:           fn.Name,
				Recipe:         mapBlock(fn.Recipe),
			}
		}
	}

	return Earthfile{
		Version:        mapVersion(tree.Version),
		SourceLocation: mapSourceLocation(tree.SourceLocation),
		Targets:        targets,
		Functions:      functions,
		BaseRecipe:     mapBlock(tree.BaseRecipe),
	}
}
