package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestAnEmptyValueIsNotSomething.
//
// `tests/wildcard-copy.earth`'s TEST function counts like this:
//
//	LET files=""
//	LET count=0
//	IF [ "$files" != "" ]
//	    SET count=$(echo "$files"|wc -l)
//	END
//
// and `echo "" | wc -l` is **1**, so a condition wrongly taken over an empty
// value produces a count of exactly one - which is what
// `+wildcard-if-exists` reports where it expects none.
func TestAnEmptyValueIsNotSomething(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

FROM alpine:3.22
WORKDIR /test

main:
    LET files=""
    IF [ "$files" != "" ]
        RUN echo TAKEN
    END
    RUN echo done
`, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		if strings.Contains(strings.Join(n.Op.Args, " "), "TAKEN") {
			t.Error(`IF [ "$files" != "" ] was taken with files empty`)
		}
	}
}

// And the other way round, so the test above cannot pass by nothing being
// planned at all.
func TestANonEmptyValueIsSomething(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

FROM alpine:3.22
WORKDIR /test

main:
    LET files="x"
    IF [ "$files" != "" ]
        RUN echo TAKEN
    END
`, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	found := false

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && strings.Contains(strings.Join(n.Op.Args, " "), "TAKEN") {
			found = true
		}
	}

	if !found {
		t.Error(`IF [ "$files" != "" ] was not taken with files set`)
	}
}
