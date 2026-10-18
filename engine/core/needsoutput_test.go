package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step whose output is its value is not served by an entry that kept none.
//
// **The bug this exists to end.** `LET v=$(ls -d helloworld*)` gave three files
// cold and nothing on every build after, silently, because a hit reproduces a
// step's effects and not its observations - and an empty string is a value, not
// an error. Twelve corpus targets counted their way to "found 0 files" with the
// files plainly in the image.
//
// Refusing the hit costs one run. Taking it costs a wrong answer on every build
// after the first.
func TestAStepWhoseOutputIsItsValueRefusesAnEntryWithout(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		what   string
		needs  bool
		entry  core.Entry
		served bool
	}{
		{"an ordinary step, an entry with nothing", false, core.Entry{}, true},
		{"an ordinary step, an entry with output", false,
			core.Entry{Stdout: "x\n", StdoutWhole: true}, true},
		{"output is the value, and it was kept whole", true,
			core.Entry{Stdout: "three\nfiles\n", StdoutWhole: true}, true},
		{"output is the value, and the step printed nothing", true,
			core.Entry{StdoutWhole: true}, true},
		{"output is the value, and the entry predates keeping it", true,
			core.Entry{}, false},
		{"output is the value, and the step printed past the bound", true,
			core.Entry{Stdout: "a partial", StdoutWhole: false}, false},
	} {
		n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, NeedsOutput: tc.needs}}

		if got := core.AnswersForTest(n, tc.entry); got != tc.served {
			t.Errorf("%s: served=%v, want %v", tc.what, got, tc.served)
		}
	}
}
