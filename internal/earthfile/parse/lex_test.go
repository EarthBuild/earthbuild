package parse

import (
	"strings"
	"testing"
)

func TestLexKeyValueCommandArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []Item
	}{
		{
			name:  "env key-value",
			input: "ENV KEY=VALUE\n",
			want: []Item{
				itemEnv(),
				itemSpace(),
				itemAtom("KEY"),
				itemAtom("="),
				itemAtom("VALUE"),
				itemNL(),
			},
		},
		{
			name:  "env key-value with leading whitespace",
			input: "  ENV KEY=VALUE\n",
			want: []Item{
				itemSpace(2),
				itemEnv(),
				itemSpace(),
				itemAtom("KEY"),
				itemAtom("="),
				itemAtom("VALUE"),
				itemNL(),
			},
		},
		{
			name:  "env with space separator",
			input: "ENV KEY VALUE\n",
			want: []Item{
				itemEnv(),
				itemSpace(),
				itemAtom("KEY"),
				itemSpace(),
				itemAtom("VALUE"),
				itemNL(),
			},
		},
		{
			name:  "env with flags",
			input: "ENV --local KEY=VALUE\n",
			want: []Item{
				itemEnv(),
				itemSpace(),
				itemAtom("--local"),
				itemSpace(),
				itemAtom("KEY"),
				itemAtom("="),
				itemAtom("VALUE"),
				itemNL(),
			},
		},
		{
			name:  "arg with invalid first char",
			input: "ARG 123KEY=VALUE\n",
			want: []Item{
				itemArg(),
				itemSpace(),
				itemError("invalid ARG key definition 123KEY"),
			},
		},
		{
			name:  "set with invalid char in key",
			input: "SET KEY-NAME=VALUE\n",
			want: []Item{
				itemSet(),
				itemSpace(),
				itemError("invalid SET key definition KEY-NAME"),
			},
		},
		{
			name:  "let with invalid char in key",
			input: "LET KEY-NAME=VALUE\n",
			want: []Item{
				itemLet(),
				itemSpace(),
				itemError("invalid LET key definition KEY-NAME"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lexer := lex("test", tt.input)

			var got []Item

			for {
				item := lexer.nextItem()
				got = append(got, item)

				if item.Typ == ItemEOF || item.Typ == ItemError {
					break
				}
			}

			for i, wantItem := range tt.want {
				if i >= len(got) {
					t.Errorf("got fewer items than expected at index %d: want %v", i, wantItem)
					break
				}

				gotItem := got[i]

				if gotItem.Typ != wantItem.Typ || gotItem.Val != wantItem.Val {
					t.Errorf("item mismatch at index %d: got {Type:%d Val:%q}, want {Type:%d Val:%q}",
						i, gotItem.Typ, gotItem.Val, wantItem.Typ, wantItem.Val)
				}
			}
		})
	}
}

func itemEnv() Item {
	return Item{Typ: ItemEnv, Val: CmdEnv}
}

func itemArg() Item {
	return Item{Typ: ItemArg, Val: CmdArg}
}

func itemSet() Item {
	return Item{Typ: ItemSet, Val: CmdSet}
}

func itemLet() Item {
	return Item{Typ: ItemLet, Val: CmdLet}
}

func itemSpace(n ...int) Item {
	count := 1
	if len(n) > 0 {
		count = n[0]
	}

	return Item{Typ: ItemWS, Val: strings.Repeat(" ", count)}
}

func itemAtom(val string) Item {
	return Item{Typ: ItemAtom, Val: val}
}

func itemNL() Item {
	return Item{Typ: ItemNL, Val: "\n"}
}

func itemError(val string) Item {
	return Item{Typ: ItemError, Val: val}
}
