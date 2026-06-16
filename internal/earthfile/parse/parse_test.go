package parse

import (
	"testing"
)

func TestParse(t *testing.T) {
	input := `VERSION 0.8

build:
    FROM alpine:3.18
    RUN echo "hello world"
    RUN echo "hello \
        world $(echo 'nested') and $VAR"
    IF [ "$VAR" = "1" ]
        RUN echo "yes"
    ELSE IF [ "$VAR" = "2" ]
        RUN echo "no"
    ELSE
        RUN echo "maybe"
    END
    FOR arg IN foo bar
        RUN echo $arg
    END
    TRY
        RUN echo "try"
    CATCH
        RUN echo "catch"
    FINALLY
        RUN echo "finally"
    END
    WITH DOCKER --pull alpine:3.18
        RUN echo "with"
    END
    WAIT
        RUN echo "wait"
    END
`
	ef, err := Parse("Earthfile", input)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	
	if ef.Version == nil || len(ef.Version.Args) != 1 || ef.Version.Args[0] != "0.8" {
		t.Errorf("Unexpected version: %+v", ef.Version)
	}

	if len(ef.Targets) != 1 {
		t.Fatalf("Expected 1 target, got %d", len(ef.Targets))
	}
	
	target := ef.Targets[0]
	if target.Name != "build:" {
		t.Errorf("Expected target name 'build:', got %q", target.Name)
	}
	
	if len(target.Recipe) != 8 {
		t.Fatalf("Expected 8 statements in recipe, got %d", len(target.Recipe))
	}
	
	stmt1 := target.Recipe[0]
	if stmt1.Command.Name != "FROM" || len(stmt1.Command.Args) != 1 || stmt1.Command.Args[0] != "alpine:3.18" {
		t.Errorf("Unexpected command 1: %+v", stmt1.Command)
	}
	
	stmt2 := target.Recipe[1]
	if stmt2.Command.Name != "RUN" || len(stmt2.Command.Args) != 2 || stmt2.Command.Args[0] != "echo" || stmt2.Command.Args[1] != "\"hello world\"" {
		t.Errorf("Unexpected command 2 args: %+v", stmt2.Command.Args)
	}

	stmt3 := target.Recipe[2]
	if stmt3.Command.Name != "RUN" || len(stmt3.Command.Args) != 2 {
		t.Fatalf("Unexpected command 3: %+v", stmt3.Command)
	}
	expectedArg := "\"hello \\\n        world $(echo 'nested') and $VAR\""
	if stmt3.Command.Args[1] != expectedArg {
		t.Errorf("Unexpected command 3 arg 1:\nexpected: %q\n     got: %q", expectedArg, stmt3.Command.Args[1])
	}

	stmt4 := target.Recipe[3]
	if stmt4.If == nil {
		t.Fatalf("Expected statement 4 to be IF block, got command: %+v", stmt4.Command)
	}
	if len(stmt4.If.Expression) != 5 {
		t.Errorf("Unexpected IF expression: %+v", stmt4.If.Expression)
	}
	if len(stmt4.If.IfBody) != 1 || stmt4.If.IfBody[0].Command.Args[0] != "echo" {
		t.Errorf("Unexpected IF body: %+v", stmt4.If.IfBody)
	}
	if len(stmt4.If.ElseIf) != 1 || stmt4.If.ElseIf[0].Expression[3] != "\"2\"" {
		t.Errorf("Unexpected ELSE IF: %+v", stmt4.If.ElseIf)
	}
	if stmt4.If.ElseBody == nil || len(*stmt4.If.ElseBody) != 1 {
		t.Errorf("Unexpected ELSE body: %+v", stmt4.If.ElseBody)
	}

	stmt5 := target.Recipe[4]
	if stmt5.For == nil {
		t.Fatalf("Expected statement 5 to be FOR block, got command: %+v", stmt5.Command)
	}
	if len(stmt5.For.Args) != 4 || stmt5.For.Args[0] != "arg" || stmt5.For.Args[1] != "IN" {
		t.Errorf("Unexpected FOR args: %+v", stmt5.For.Args)
	}
	if len(stmt5.For.Body) != 1 || stmt5.For.Body[0].Command.Args[0] != "echo" {
		t.Errorf("Unexpected FOR body: %+v", stmt5.For.Body)
	}

	stmt6 := target.Recipe[5]
	if stmt6.Try == nil {
		t.Fatalf("Expected TRY block, got command: %+v", stmt6.Command)
	}
	if len(stmt6.Try.TryBody) != 1 || stmt6.Try.CatchBody == nil || stmt6.Try.FinallyBody == nil {
		t.Errorf("Unexpected TRY block bodies: %+v", stmt6.Try)
	}

	stmt7 := target.Recipe[6]
	if stmt7.With == nil {
		t.Fatalf("Expected WITH block, got command: %+v", stmt7.Command)
	}
	if stmt7.With.Command.Name != "DOCKER" || len(stmt7.With.Command.Args) != 2 {
		t.Errorf("Unexpected WITH command: %+v", stmt7.With.Command)
	}

	stmt8 := target.Recipe[7]
	if stmt8.Wait == nil {
		t.Fatalf("Expected WAIT block, got command: %+v", stmt8.Command)
	}
	if len(stmt8.Wait.Body) != 1 {
		t.Errorf("Unexpected WAIT body: %+v", stmt8.Wait.Body)
	}
}
