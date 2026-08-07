package earthfile

import (
	"io"
	"strings"
)

// Format writes the Earthfile AST as formatted Earthfile source code to w.
func (t *Tree) Format(w io.Writer) error {
	pw := &errWriter{w: w}

	pw.writeString("\n")

	if t.Version != nil {
		pw.writeString(string(CmdVersion))

		if len(t.Version.Args) > 0 {
			pw.writeString(" ")
			pw.writeString(strings.Join(t.Version.Args, " "))
		}

		pw.writeString("\n")

		if len(t.BaseRecipe) > 0 || len(t.Functions) > 0 || len(t.Targets) > 0 {
			pw.writeString("\n")
		}
	}

	if len(t.BaseRecipe) > 0 {
		for _, stmt := range t.BaseRecipe {
			pw.formatStatement(stmt, 0)
		}

		if len(t.Functions) > 0 || len(t.Targets) > 0 {
			pw.writeString("\n")
		}
	}

	for i, fn := range t.Functions {
		pw.formatFunction(fn)

		if i < len(t.Functions)-1 || len(t.Targets) > 0 {
			pw.writeString("\n")
		}
	}

	for i, target := range t.Targets {
		if target.Docs != "" {
			pw.writeString(target.Docs)

			if !strings.HasSuffix(target.Docs, "\n") {
				pw.writeString("\n")
			}
		}

		pw.writeString(target.Name)
		pw.writeString(":\n")

		for _, stmt := range target.Recipe {
			pw.formatStatement(stmt, 1)
		}

		if i < len(t.Targets)-1 {
			pw.writeString("\n")
		}
	}

	return pw.err
}

type errWriter struct {
	w   io.Writer
	err error
}

func (ew *errWriter) writeString(s string) {
	if ew.err != nil {
		return
	}

	_, err := io.WriteString(ew.w, s)
	if err != nil {
		ew.err = err
	}
}

func (ew *errWriter) indent(depth int) {
	for range depth {
		ew.writeString("\t")
	}
}

func (ew *errWriter) formatFunction(fn Function) {
	ew.writeString("FUNCTION ")
	ew.writeString(fn.Name)
	ew.writeString(":\n")

	for _, stmt := range fn.Recipe {
		ew.formatStatement(stmt, 1)
	}
}

func (ew *errWriter) formatStatement(stmt Statement, depth int) {
	switch {
	case stmt.Command != nil:
		ew.formatCommand(stmt.Command, depth)
	case stmt.With != nil:
		ew.formatWith(stmt.With, depth)
	case stmt.If != nil:
		ew.formatIf(stmt.If, depth)
	case stmt.Try != nil:
		ew.formatTry(stmt.Try, depth)
	case stmt.For != nil:
		ew.formatFor(stmt.For, depth)
	case stmt.Wait != nil:
		ew.formatWait(stmt.Wait, depth)
	}
}

func (ew *errWriter) formatCommand(cmd *Command, depth int) {
	if cmd.Docs != "" {
		ew.indent(depth)
		ew.writeString(cmd.Docs)

		if !strings.HasSuffix(cmd.Docs, "\n") {
			ew.writeString("\n")
		}
	}

	if cmd.Name == "" {
		return
	}

	if cmd.Name == CmdFromDockerfile {
		ew.indent(depth)
		ew.writeString("FROM DOCKERFILE")

		if len(cmd.Args) == 1 {
			ew.writeString(" ")
			ew.writeString(cmd.Args[0])
			ew.writeString("\n")

			return
		}

		if len(cmd.Args) > 1 {
			ew.writeString(" \\")

			for i, arg := range cmd.Args {
				ew.writeString("\n")
				ew.indent(depth + 1)
				ew.writeString(arg)

				if i < len(cmd.Args)-1 {
					ew.writeString(" \\")
				}
			}
		}

		ew.writeString("\n")

		return
	}

	ew.indent(depth)
	ew.writeString(string(cmd.Name))

	if cmd.ExecMode {
		ew.writeString(" --exec")
	}

	if len(cmd.Args) > 0 {
		ew.writeString(" ")
		ew.writeString(strings.Join(cmd.Args, " "))
	}

	ew.writeString("\n")
}

func (ew *errWriter) formatWith(w *WithStatement, depth int) {
	ew.indent(depth)
	ew.writeString("WITH ")
	ew.writeString(string(w.Command.Name))

	if len(w.Command.Args) > 0 {
		ew.writeString(" ")
		ew.writeString(strings.Join(w.Command.Args, " "))
	}

	ew.writeString("\n")

	for _, stmt := range w.Body {
		ew.formatStatement(stmt, depth+1)
	}

	ew.indent(depth)
	ew.writeString("END\n")
}

func (ew *errWriter) formatIf(ifStmt *IfStatement, depth int) {
	ew.indent(depth)
	ew.writeString("IF")

	if ifStmt.ExecMode {
		ew.writeString(" --exec")
	}

	if len(ifStmt.Expression) > 0 {
		ew.writeString(" ")
		ew.writeString(strings.Join(ifStmt.Expression, " "))
	}

	ew.writeString("\n")

	for _, stmt := range ifStmt.IfBody {
		ew.formatStatement(stmt, depth+1)
	}

	for _, elseIf := range ifStmt.ElseIf {
		ew.indent(depth)
		ew.writeString("ELSE IF")

		if elseIf.ExecMode {
			ew.writeString(" --exec")
		}

		if len(elseIf.Expression) > 0 {
			ew.writeString(" ")
			ew.writeString(strings.Join(elseIf.Expression, " "))
		}

		ew.writeString("\n")

		for _, stmt := range elseIf.Body {
			ew.formatStatement(stmt, depth+1)
		}
	}

	if ifStmt.ElseBody != nil {
		ew.indent(depth)
		ew.writeString("ELSE\n")

		for _, stmt := range *ifStmt.ElseBody {
			ew.formatStatement(stmt, depth+1)
		}
	}

	ew.indent(depth)
	ew.writeString("END\n")
}

func (ew *errWriter) formatTry(tryStmt *TryStatement, depth int) {
	ew.indent(depth)
	ew.writeString("TRY\n")

	for _, stmt := range tryStmt.TryBody {
		ew.formatStatement(stmt, depth+1)
	}

	if tryStmt.CatchBody != nil {
		ew.indent(depth)
		ew.writeString("CATCH\n")

		for _, stmt := range *tryStmt.CatchBody {
			ew.formatStatement(stmt, depth+1)
		}
	}

	if tryStmt.FinallyBody != nil {
		ew.indent(depth)
		ew.writeString("FINALLY\n")

		for _, stmt := range *tryStmt.FinallyBody {
			ew.formatStatement(stmt, depth+1)
		}
	}

	ew.indent(depth)
	ew.writeString("END\n")
}

func (ew *errWriter) formatFor(forStmt *ForStatement, depth int) {
	ew.indent(depth)
	ew.writeString("FOR")

	if len(forStmt.Args) > 0 {
		ew.writeString(" ")
		ew.writeString(strings.Join(forStmt.Args, " "))
	}

	ew.writeString("\n")

	for _, stmt := range forStmt.Body {
		ew.formatStatement(stmt, depth+1)
	}

	ew.indent(depth)
	ew.writeString("END\n")
}

func (ew *errWriter) formatWait(waitStmt *WaitStatement, depth int) {
	ew.indent(depth)
	ew.writeString("WAIT")

	if len(waitStmt.Args) > 0 {
		ew.writeString(" ")
		ew.writeString(strings.Join(waitStmt.Args, " "))
	}

	ew.writeString("\n")

	for _, stmt := range waitStmt.Body {
		ew.formatStatement(stmt, depth+1)
	}

	ew.indent(depth)
	ew.writeString("END\n")
}
