package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A holder that will not dial becomes a source that says so.
//
// Skipping it silently is right for the build - somebody else may have the layer
// - and wrong for anybody trying to find out why nothing was fetched. A worker
// whose one holder would not parse ends up with no sources at all and reports
// "no source had it", which is true and useless (E309).
//
// Carried as a source rather than logged, because `Provision` keeps the last
// source's reason and that is what reaches the refusal the driver prints. So the
// assertion is that the reason survives being *fetched from*, not merely that
// something was appended: a source that swallows its own error would satisfy a
// count and lose the sentence.
func TestAHolderThatWillNotDialBecomesASourceThatSaysSo(t *testing.T) {
	t.Parallel()

	refused := errors.New("the address has no port")

	c := &runnerCfg{
		dial: func(string) (Source, error) { return nil, refused },
	}

	got := c.sources(Assignment{Hints: Hints{Holders: []string{"worker-3@nowhere"}}})

	if len(got) != 1 {
		t.Fatalf("a holder that would not dial produced %d source(s): with"+
			" none, the build reports that no source had the layer, which is"+
			" true and says nothing about the address that failed", len(got))
	}

	if !strings.Contains(got[0].Name(), "worker-3") {
		t.Errorf("the source is named %q and does not name the holder",
			got[0].Name())
	}

	_, err := got[0].Fetch(context.Background(), nil)
	if !errors.Is(err, refused) {
		t.Errorf("fetching from the unreachable holder gave %v, want the dial"+
			" failure: the reason has to survive as far as the refusal the"+
			" driver prints", err)
	}
}
