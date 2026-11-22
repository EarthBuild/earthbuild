package exec

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step's standard output is kept on its result.
//
// **Because a hit reproduces a step's effects and not its observations.** `LET
// v=$(cmd)` is the command's output, so a result that does not carry it is a
// result a cache hit cannot answer with - which is why every command
// substitution was marked uncacheable and re-runs forever.
func TestAStepsStandardOutputIsKept(t *testing.T) {
	e := &Executor{}

	write, flush, stdout := e.sinkFor(&ir.Node{})
	if write == nil {
		t.Fatal("no sink, so nothing is recorded even though recording is on")
	}

	write("three\nfiles\n", false)
	write("a warning\n", true) // standard error is not the value
	flush()

	got, whole := stdout()
	if got != "three\nfiles\n" {
		t.Errorf("kept %q, want the two lines of standard output alone", got)
	}

	if !whole {
		t.Error("a short output was reported as incomplete")
	}
}

// Past the bound it stops, and says so.
//
// **A truncated substitution is a wrong value, not a partial one.** A caller
// reading half of `$(ls)` gets a list that looks complete and is not, so the
// answer has to be "run it again" rather than "here is some of it".
func TestOutputPastTheBoundIsNotWhole(t *testing.T) {
	e := &Executor{}

	write, flush, stdout := e.sinkFor(&ir.Node{})

	write(strings.Repeat("x", maxRecordedOutput+1)+"\n", false)
	flush()

	if _, whole := stdout(); whole {
		t.Error("an output past the bound was reported as whole, so a caller" +
			"\n  would read a truncated value as the command's answer")
	}
}

// Switched off, nothing is kept.
func TestRecordingCanBeTurnedOff(t *testing.T) {
	t.Setenv(EnvRecordOutput, "0")

	e := &Executor{}

	write, flush, stdout := e.sinkFor(&ir.Node{})
	if write != nil {
		write("secret-ish\n", false)
		flush()
	}

	if got, _ := stdout(); got != "" {
		t.Errorf("kept %q with recording off", got)
	}
}
