package ast

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/EarthBuild/earthbuild/ast/parser"
	"github.com/EarthBuild/earthbuild/ast/spec"
	"github.com/antlr4-go/antlr/v4"
	"github.com/pkg/errors"
)

var _ parser.EarthParserListener = &listener{}

type block struct {
	statement     *spec.Statement
	withStatement *spec.WithStatement
	ifStatement   *spec.IfStatement
	elseIf        *spec.ElseIf
	tryStatement  *spec.TryStatement
	forStatement  *spec.ForStatement
	waitStatement *spec.WaitStatement
	block         spec.Block
}

type listener struct {
	*parser.BaseEarthParserListener

	err             error
	tokStream       *antlr.CommonTokenStream
	ef              *spec.Earthfile
	target          *spec.Target
	function        *spec.Function
	command         *spec.Command
	filePath        string
	blocks          []*block
	stmtWords       []string
	enableSourceMap bool
	execMode        bool
}

func newListener(stream *antlr.CommonTokenStream, filePath string, enableSourceMap bool) *listener {
	ef := &spec.Earthfile{}
	if enableSourceMap {
		ef.SourceLocation = &spec.SourceLocation{
			File: filePath,
		}
	}

	return &listener{
		tokStream:       stream,
		filePath:        filePath,
		enableSourceMap: enableSourceMap,
		ef:              ef,
	}
}

func (l *listener) Err() error {
	if len(l.blocks) != 0 && l.err == nil {
		return errors.New("parsing did not finish")
	}

	return l.err
}

func (l *listener) Earthfile() spec.Earthfile {
	return *l.ef
}

func (l *listener) block() *block {
	return l.blocks[len(l.blocks)-1]
}

func (l *listener) pushNewBlock() {
	l.blocks = append(l.blocks, new(block))
}

func (l *listener) popBlock() spec.Block {
	ret := l.block().block
	l.blocks = l.blocks[:len(l.blocks)-1]

	return ret
}

func (l *listener) docs(c antlr.ParserRuleContext) string {
	comments := l.tokStream.GetHiddenTokensToLeft(c.GetStart().GetTokenIndex(), parser.EarthLexerCOMMENTS_CHANNEL)

	var (
		docs        strings.Builder
		leadingTrim string
		once        sync.Once
	)

	for _, c := range comments {
		line := strings.TrimSpace(c.GetText())
		line = strings.TrimPrefix(line, "#")

		once.Do(func() {
			var trimRunes []rune

			for _, r := range line {
				if unicode.IsSpace(r) {
					trimRunes = append(trimRunes, r)
					continue
				}

				break
			}

			leadingTrim = string(trimRunes)
		})

		line = strings.TrimPrefix(line, leadingTrim)
		docs.WriteString(line)
		docs.WriteByte('\n')
	}

	return docs.String()
}

// Base -----------------------------------------------------------------------

func (l *listener) EnterEarthFile(*parser.EarthFileContext) {
	l.pushNewBlock()
}

func (l *listener) ExitEarthFile(*parser.EarthFileContext) {
	l.ef.BaseRecipe = l.popBlock()
}

// Target ---------------------------------------------------------------------

func (l *listener) EnterTarget(c *parser.TargetContext) {
	l.target = new(spec.Target)
	if l.enableSourceMap {
		l.target.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}

	l.pushNewBlock()
}

func (l *listener) EnterTargetHeader(c *parser.TargetHeaderContext) {
	l.target.Name = strings.TrimSuffix(c.Target().GetText(), ":")
	l.target.Docs = l.docs(c)
}

func (l *listener) ExitTarget(*parser.TargetContext) {
	l.target.Recipe = l.popBlock()
	l.ef.Targets = append(l.ef.Targets, *l.target)
	l.target = nil
}

// User command ---------------------------------------------------------------

func (l *listener) EnterUserCommand(c *parser.UserCommandContext) {
	l.function = new(spec.Function)
	if l.enableSourceMap {
		l.function.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}

	l.pushNewBlock()
}

func (l *listener) EnterUserCommandHeader(c *parser.UserCommandHeaderContext) {
	l.function.Name = strings.TrimSuffix(c.GetText(), ":")
}

func (l *listener) ExitUserCommand(*parser.UserCommandContext) {
	l.function.Recipe = l.popBlock()
	l.ef.Functions = append(l.ef.Functions, *l.function)
	l.function = nil
}

// Function ---------------------------------------------------------------

func (l *listener) EnterFunction(c *parser.FunctionContext) {
	l.function = new(spec.Function)
	if l.enableSourceMap {
		l.function.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}

	l.pushNewBlock()
}

func (l *listener) EnterFunctionHeader(c *parser.FunctionHeaderContext) {
	l.function.Name = strings.TrimSuffix(c.GetText(), ":")
}

func (l *listener) ExitFunction(*parser.FunctionContext) {
	l.function.Recipe = l.popBlock()
	l.ef.Functions = append(l.ef.Functions, *l.function)
	l.function = nil
}

// Statement ------------------------------------------------------------------

func (l *listener) EnterStmt(c *parser.StmtContext) {
	l.block().statement = new(spec.Statement)
	if l.enableSourceMap {
		l.block().statement.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}
}

func (l *listener) ExitStmt(*parser.StmtContext) {
	l.block().block = append(l.block().block, *l.block().statement)
	l.block().statement = nil
}

// Command --------------------------------------------------------------------

func (l *listener) EnterCommandStmt(c *parser.CommandStmtContext) {
	l.command = &spec.Command{
		Docs: l.docs(c),
	}
	if l.enableSourceMap {
		l.command.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}

	l.stmtWords = []string{}
	l.execMode = false
}

func (l *listener) ExitCommandStmt(*parser.CommandStmtContext) {
	l.command.Args = l.stmtWords
	l.command.ExecMode = l.execMode
	l.block().statement.Command = l.command
	l.command = nil
}

// Individual commands --------------------------------------------------------

func (l *listener) EnterFromStmt(*parser.FromStmtContext) {
	l.command.Name = "FROM"
}

func (l *listener) EnterFromDockerfileStmt(*parser.FromDockerfileStmtContext) {
	l.command.Name = "FROM DOCKERFILE"
}

func (l *listener) EnterLocallyStmt(*parser.LocallyStmtContext) {
	l.command.Name = "LOCALLY"
}

func (l *listener) EnterCopyStmt(*parser.CopyStmtContext) {
	l.command.Name = "COPY"
}

func (l *listener) EnterRunStmt(*parser.RunStmtContext) {
	l.command.Name = "RUN"
}

func (l *listener) EnterSaveArtifact(*parser.SaveArtifactContext) {
	l.command.Name = "SAVE ARTIFACT"
}

func (l *listener) EnterSaveImage(*parser.SaveImageContext) {
	l.command.Name = "SAVE IMAGE"
}

func (l *listener) EnterBuildStmt(*parser.BuildStmtContext) {
	l.command.Name = "BUILD"
}

func (l *listener) EnterWorkdirStmt(*parser.WorkdirStmtContext) {
	l.command.Name = "WORKDIR"
}

func (l *listener) EnterUserStmt(*parser.UserStmtContext) {
	l.command.Name = "USER"
}

func (l *listener) EnterCmdStmt(*parser.CmdStmtContext) {
	l.command.Name = "CMD"
}

func (l *listener) EnterEntrypointStmt(*parser.EntrypointStmtContext) {
	l.command.Name = "ENTRYPOINT"
}

func (l *listener) EnterExposeStmt(*parser.ExposeStmtContext) {
	l.command.Name = "EXPOSE"
}

func (l *listener) EnterVolumeStmt(*parser.VolumeStmtContext) {
	l.command.Name = "VOLUME"
}

func (l *listener) EnterEnvStmt(*parser.EnvStmtContext) {
	l.command.Name = "ENV"
}

func (l *listener) EnterArgStmt(*parser.ArgStmtContext) {
	l.command.Name = "ARG"
}

func (l *listener) EnterSetStmt(*parser.SetStmtContext) {
	l.command.Name = "SET"
}

func (l *listener) EnterLetStmt(*parser.LetStmtContext) {
	l.command.Name = "LET"
}

func (l *listener) EnterLabelStmt(*parser.LabelStmtContext) {
	l.command.Name = "LABEL"
}

func (l *listener) EnterGitCloneStmt(*parser.GitCloneStmtContext) {
	l.command.Name = "GIT CLONE"
}

func (l *listener) EnterHealthcheckStmt(*parser.HealthcheckStmtContext) {
	l.command.Name = "HEALTHCHECK"
}

func (l *listener) EnterAddStmt(*parser.AddStmtContext) {
	l.command.Name = "ADD"
}

func (l *listener) EnterStopsignalStmt(*parser.StopsignalStmtContext) {
	l.command.Name = "STOP SIGNAL"
}

func (l *listener) EnterOnbuildStmt(*parser.OnbuildStmtContext) {
	l.command.Name = "ONBUILD"
}

func (l *listener) EnterShellStmt(*parser.ShellStmtContext) {
	l.command.Name = "SHELL"
}

func (l *listener) EnterUserCommandStmt(*parser.UserCommandStmtContext) {
	l.command.Name = "COMMAND"
}

func (l *listener) EnterFunctionStmt(*parser.FunctionStmtContext) {
	l.command.Name = "FUNCTION"
}

func (l *listener) EnterDoStmt(*parser.DoStmtContext) {
	l.command.Name = "DO"
}

func (l *listener) EnterImportStmt(*parser.ImportStmtContext) {
	l.command.Name = "IMPORT"
}

func (l *listener) EnterCacheStmt(*parser.CacheStmtContext) {
	l.command.Name = "CACHE"
}

func (l *listener) EnterHostStmt(*parser.HostStmtContext) {
	l.command.Name = "HOST"
}

func (l *listener) EnterProjectStmt(*parser.ProjectStmtContext) {
	l.command.Name = "PROJECT"
}

// With -----------------------------------------------------------------------

func (l *listener) EnterWithStmt(c *parser.WithStmtContext) {
	l.block().withStatement = new(spec.WithStatement)
	if l.enableSourceMap {
		l.block().withStatement.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}
}

func (l *listener) ExitWithStmt(*parser.WithStmtContext) {
	l.block().statement.With = l.block().withStatement
	l.block().withStatement = nil
}

// withBlock ------------------------------------------------------------------

func (l *listener) EnterWithBlock(*parser.WithBlockContext) {
	l.pushNewBlock()
}

func (l *listener) ExitWithBlock(*parser.WithBlockContext) {
	withBlock := l.popBlock()
	l.block().withStatement.Body = withBlock
}

// withCommand ----------------------------------------------------------------

func (l *listener) EnterWithCommand(c *parser.WithCommandContext) {
	l.command = new(spec.Command)
	if l.enableSourceMap {
		l.command.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}

	l.stmtWords = []string{}
	l.execMode = false
}

func (l *listener) ExitWithCommand(*parser.WithCommandContext) {
	l.command.Args = l.stmtWords
	l.command.ExecMode = l.execMode
	l.block().withStatement.Command = *l.command
	l.command = nil
}

// Individual with commands ---------------------------------------------------

func (l *listener) EnterDockerCommand(*parser.DockerCommandContext) {
	l.command.Name = "DOCKER"
}

// If -------------------------------------------------------------------------

func (l *listener) EnterIfStmt(c *parser.IfStmtContext) {
	l.block().ifStatement = new(spec.IfStatement)
	if l.enableSourceMap {
		l.block().ifStatement.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}
}

func (l *listener) ExitIfStmt(*parser.IfStmtContext) {
	l.block().statement.If = l.block().ifStatement
	l.block().ifStatement = nil
}

func (l *listener) EnterIfExpr(*parser.IfExprContext) {
	l.stmtWords = []string{}
	l.execMode = false
}

func (l *listener) ExitIfExpr(*parser.IfExprContext) {
	l.block().ifStatement.Expression = l.stmtWords
	l.block().ifStatement.ExecMode = l.execMode
}

func (l *listener) EnterIfBlock(*parser.IfBlockContext) {
	l.pushNewBlock()
}

func (l *listener) ExitIfBlock(*parser.IfBlockContext) {
	ifBlock := l.popBlock()
	l.block().ifStatement.IfBody = ifBlock
}

func (l *listener) EnterElseIfClause(c *parser.ElseIfClauseContext) {
	l.block().elseIf = new(spec.ElseIf)
	if l.enableSourceMap {
		l.block().elseIf.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}
}

func (l *listener) ExitElseIfClause(*parser.ElseIfClauseContext) {
	l.block().ifStatement.ElseIf = append(l.block().ifStatement.ElseIf, *l.block().elseIf)
	l.block().elseIf = nil
}

func (l *listener) EnterElseIfExpr(*parser.ElseIfExprContext) {
	l.stmtWords = []string{}
	l.execMode = false
}

func (l *listener) ExitElseIfExpr(*parser.ElseIfExprContext) {
	l.block().elseIf.Expression = l.stmtWords
	l.block().elseIf.ExecMode = l.execMode
}

func (l *listener) EnterElseIfBlock(*parser.ElseIfBlockContext) {
	l.pushNewBlock()
}

func (l *listener) ExitElseIfBlock(*parser.ElseIfBlockContext) {
	elseIfBlock := l.popBlock()
	l.block().elseIf.Body = elseIfBlock
}

func (l *listener) EnterElseBlock(*parser.ElseBlockContext) {
	l.pushNewBlock()
}

func (l *listener) ExitElseBlock(*parser.ElseBlockContext) {
	elseBlock := l.popBlock()
	l.block().ifStatement.ElseBody = &elseBlock
}

// Try -------------------------------------------------------------------------

func (l *listener) EnterTryStmt(c *parser.TryStmtContext) {
	l.block().tryStatement = new(spec.TryStatement)
	if l.enableSourceMap {
		l.block().tryStatement.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}
}

func (l *listener) ExitTryStmt(*parser.TryStmtContext) {
	l.block().statement.Try = l.block().tryStatement
	l.block().tryStatement = nil
}

func (l *listener) EnterTryBlock(*parser.TryBlockContext) {
	l.pushNewBlock()
}

func (l *listener) ExitTryBlock(*parser.TryBlockContext) {
	tryBlock := l.popBlock()
	l.block().tryStatement.TryBody = tryBlock
}

func (l *listener) EnterCatchBlock(*parser.CatchBlockContext) {
	l.pushNewBlock()
}

func (l *listener) ExitCatchBlock(*parser.CatchBlockContext) {
	catchBlock := l.popBlock()
	l.block().tryStatement.CatchBody = &catchBlock
}

func (l *listener) EnterFinallyBlock(*parser.FinallyBlockContext) {
	l.pushNewBlock()
}

func (l *listener) ExitFinallyBlock(*parser.FinallyBlockContext) {
	finallyBlock := l.popBlock()
	l.block().tryStatement.FinallyBody = &finallyBlock
}

// For ------------------------------------------------------------------------

func (l *listener) EnterForStmt(c *parser.ForStmtContext) {
	l.block().forStatement = new(spec.ForStatement)
	if l.enableSourceMap {
		l.block().forStatement.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}
}

func (l *listener) ExitForStmt(*parser.ForStmtContext) {
	l.block().statement.For = l.block().forStatement
	l.block().forStatement = nil
}

func (l *listener) EnterForExpr(*parser.ForExprContext) {
	l.stmtWords = []string{}
}

func (l *listener) ExitForExpr(*parser.ForExprContext) {
	l.block().forStatement.Args = l.stmtWords
}

func (l *listener) EnterForBlock(*parser.ForBlockContext) {
	l.pushNewBlock()
}

func (l *listener) ExitForBlock(*parser.ForBlockContext) {
	forBlock := l.popBlock()
	l.block().forStatement.Body = forBlock
}

// Wait -----------------------------------------------------------------------

func (l *listener) EnterWaitStmt(c *parser.WaitStmtContext) {
	l.block().waitStatement = new(spec.WaitStatement)
	if l.enableSourceMap {
		l.block().waitStatement.SourceLocation = &spec.SourceLocation{
			File:        l.filePath,
			StartLine:   c.GetStart().GetLine(),
			StartColumn: c.GetStart().GetColumn(),
			EndLine:     c.GetStop().GetLine(),
			EndColumn:   c.GetStop().GetColumn(),
		}
	}
}

func (l *listener) ExitWaitStmt(*parser.WaitStmtContext) {
	l.block().statement.Wait = l.block().waitStatement
	l.block().waitStatement = nil
}

func (l *listener) EnterWaitExpr(*parser.WaitExprContext) {
	l.stmtWords = []string{}
}

func (l *listener) ExitWaitExpr(*parser.WaitExprContext) {
	l.block().waitStatement.Args = l.stmtWords
}

func (l *listener) EnterWaitBlock(*parser.WaitBlockContext) {
	l.pushNewBlock()
}

func (l *listener) ExitWaitBlock(*parser.WaitBlockContext) {
	waitBlock := l.popBlock()
	l.block().waitStatement.Body = waitBlock
}

// EnvArgKey, EnvArgValue, LabelKey, LabelValue -------------------------------

func (l *listener) EnterEnvArgKey(c *parser.EnvArgKeyContext) {
	err := checkEnvVarName(c.GetText())
	if err != nil {
		l.err = err
		return
	}

	l.stmtWords = append(l.stmtWords, c.GetText())
}

func (l *listener) EnterEnvArgValue(c *parser.EnvArgValueContext) {
	l.stmtWords = append(l.stmtWords, "=", c.GetText())
}

func (l *listener) EnterLabelKey(c *parser.LabelKeyContext) {
	l.stmtWords = append(l.stmtWords, c.GetText())
}

func (l *listener) EnterLabelValue(c *parser.LabelValueContext) {
	l.stmtWords = append(l.stmtWords, "=", c.GetText())
}

// StmtWord -------------------------------------------------------------------

func (l *listener) ExitStmtWordsMaybeJSON(c *parser.StmtWordsMaybeJSONContext) {
	// Try to parse as JSON. If parse works, override the already collected stmtWords.
	var words []string

	err := json.Unmarshal([]byte(c.GetText()), &words)
	if err == nil {
		l.stmtWords = words
		l.execMode = true
	}
}

func (l *listener) EnterStmtWord(c *parser.StmtWordContext) {
	l.stmtWords = append(l.stmtWords, replaceEscape(c.GetText()))
}

// ----------------------------------------------------------------------------

func checkEnvVarName(str string) error {
	if !IsValidEnvVarName(str) {
		return errors.Errorf("invalid env key definition %s", str)
	}

	return nil
}

var lineContinuationRegexp = regexp.MustCompile(`\\[ \t]*(#[^\n\r]*)?(\n|(\r\n))[\t ]*((#[^\n\r]*)?(\n|(\r\n))[\t ]*)*`)

func replaceEscape(str string) string {
	return lineContinuationRegexp.ReplaceAllString(str, "")
}
