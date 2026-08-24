package cmdopts_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
)

// Every option `WITH DOCKER` accepts is mentioned in the language reference.
//
// **A weak check, deliberately, and it catches the failure that actually
// happens.** Presence in the document is not correctness of the description -
// nothing here can test that. What it catches is an option added to the parser
// and to nothing else, which is a flag users can type, that changes what a build
// does, and that no reader can discover. `--isolate` was one edit away from
// being exactly that.
//
// Scoped to `WITH DOCKER` because that is the construct whose options this work
// changed, and because a guard that starts red teaches nobody anything: the
// others would need auditing first, and an audit is not this test's job.
func TestEveryWithDockerOptionIsInTheReference(t *testing.T) {
	t.Parallel()

	ref, err := os.ReadFile("../../docs/earthfile/earthfile.md")
	if err != nil {
		t.Fatalf("the language reference is not where this test expects it: %v", err)
	}

	rt := reflect.TypeFor[cmdopts.WithDocker]()

	for field := range rt.Fields() {
		long := field.Tag.Get("long")
		if long == "" {
			t.Errorf("%s has no long flag, so nothing can be written about it",
				field.Name)

			continue
		}

		// The backtick matters: `--load` in prose is a mention, and this is
		// looking for the option written as an option.
		if !strings.Contains(string(ref), "`--"+long) {
			t.Errorf("WITH DOCKER --%s is accepted by the parser and appears"+
				" nowhere in docs/earthfile/earthfile.md:\n"+
				"  a flag a user can type, that changes what a build does, and"+
				" that no reader can discover", long)
		}
	}
}
