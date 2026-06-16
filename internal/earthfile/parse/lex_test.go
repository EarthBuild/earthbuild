package parse

import (
	"github.com/google/go-cmp/cmp"
	"testing"
)

type lexTest struct {
	name   string
	input  string
	tokens []Item
}

var lexTests = []lexTest{
	{"empty", "", []Item{{ItemEOF, 0, "", 1, 1}}},
	{"spaces", " \t \n", []Item{
		{ItemWS, 0, " \t ", 1, 1},
		{ItemNL, 3, "\n", 1, 4},
		{ItemEOF, 4, "", 2, 1},
	}},
	{"target", "build:\n", []Item{
		{ItemTarget, 0, "build:", 1, 1},
		{ItemNL, 6, "\n", 1, 7},
		{ItemEOF, 7, "", 2, 1},
	}},
	{"comment", "# hello world\n", []Item{
		{ItemComment, 0, "# hello world", 1, 1},
		{ItemNL, 13, "\n", 1, 14},
		{ItemEOF, 14, "", 2, 1},
	}},
	{"version", "VERSION 0.8\n", []Item{
		{ItemVersion, 0, "VERSION", 1, 1},
		{ItemWS, 7, " ", 1, 8},
		{ItemAtom, 8, "0.8", 1, 9},
		{ItemNL, 11, "\n", 1, 12},
		{ItemEOF, 12, "", 2, 1},
	}},
}

// collect gathers the emitted items into a slice.
func collect(t *lexTest) (items []Item) {
	l := lex(t.name, t.input)
	for {
		item := l.nextItem()
		items = append(items, item)
		if item.Typ == ItemEOF || item.Typ == ItemError {
			break
		}
	}
	return
}

func TestLex(t *testing.T) {
	for _, tc := range lexTests {
		t.Run(tc.name, func(t *testing.T) {
			items := collect(&tc)
			if diff := cmp.Diff(tc.tokens, items); diff != "" {
				t.Errorf("%s: tokens mismatch (-want +got):\n%s", tc.name, diff)
			}
		})
	}
}
