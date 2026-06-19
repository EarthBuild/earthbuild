package inputgraph

import (
	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

func (l *loader) hashIfStatement(s earthfile.IfStatement) {
	l.hasher.HashString("IF")
	l.hasher.HashJSONMarshalled(s.Expression)
	l.hasher.HashBool(s.ExecMode)
	l.hasher.HashInt(len(s.IfBody))
	l.hasher.HashInt(len(s.ElseIf))

	if s.ElseBody != nil {
		l.hasher.HashInt(len(*s.ElseBody))
	}
}

func (l *loader) hashElseIf(e earthfile.ElseIf) {
	l.hasher.HashString("ELSE IF")
	l.hasher.HashJSONMarshalled(e.Expression)
	l.hasher.HashBool(e.ExecMode)
	l.hasher.HashInt(len(e.Body))
}

func (l *loader) hashWaitStatement(w earthfile.WaitStatement) {
	l.hasher.HashString("WAIT")
	l.hasher.HashInt(len(w.Body))
	l.hasher.HashJSONMarshalled(w.Args)
}

func (l *loader) hashVersion(v earthfile.Version) {
	l.hasher.HashString("VERSION")
	l.hasher.HashJSONMarshalled(v.Args)
}

func (l *loader) hashCommand(c earthfile.Command) {
	l.hasher.HashString(string(c.Name))
	l.hasher.HashJSONMarshalled(c.Args)
	l.hasher.HashBool(c.ExecMode)
}

func (l *loader) hashForStatement(f earthfile.ForStatement) {
	l.hasher.HashString("FOR")
	l.hasher.HashJSONMarshalled(f.Args)
}

func (l *loader) hashTryStatement() {
	l.hasher.HashString("TRY")
}
