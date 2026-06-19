package earthfile

import (
	"strings"
	"testing"
)

func TestLex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []item
	}{
		// Key-Value Command Args
		{
			name:  "env key-value",
			input: "ENV KEY=VALUE\n",
			want: []item{
				makeItemEnv(),
				makeItemSpace(),
				makeItemAtom("KEY"),
				makeItemAtom("="),
				makeItemAtom("VALUE"),
				makeItemNL(),
				makeItemEOF(),
			},
		},
		{
			name:  "env key-value with leading whitespace",
			input: "  ENV KEY=VALUE\n",
			want: []item{
				makeItemSpace(2),
				makeItemEnv(),
				makeItemSpace(),
				makeItemAtom("KEY"),
				makeItemAtom("="),
				makeItemAtom("VALUE"),
				makeItemNL(),
				makeItemEOF(),
			},
		},
		{
			name:  "env with space separator",
			input: "ENV KEY VALUE\n",
			want: []item{
				makeItemEnv(),
				makeItemSpace(),
				makeItemAtom("KEY"),
				makeItemSpace(),
				makeItemAtom("VALUE"),
				makeItemNL(),
				makeItemEOF(),
			},
		},
		{
			name:  "env with flags",
			input: "ENV --local KEY=VALUE\n",
			want: []item{
				makeItemEnv(),
				makeItemSpace(),
				makeItemAtom("--local"),
				makeItemSpace(),
				makeItemAtom("KEY"),
				makeItemAtom("="),
				makeItemAtom("VALUE"),
				makeItemNL(),
				makeItemEOF(),
			},
		},
		{
			name:  "arg with invalid first char",
			input: "ARG 123KEY=VALUE\n",
			want: []item{
				makeItemArg(),
				makeItemSpace(),
				makeItemError("invalid ARG key definition 123KEY"),
			},
		},
		{
			name:  "set with invalid char in key",
			input: "SET KEY-NAME=VALUE\n",
			want: []item{
				makeItemSet(),
				makeItemSpace(),
				makeItemError("invalid SET key definition KEY-NAME"),
			},
		},
		{
			name:  "let with invalid char in key",
			input: "LET KEY-NAME=VALUE\n",
			want: []item{
				makeItemLet(),
				makeItemSpace(),
				makeItemError("invalid LET key definition KEY-NAME"),
			},
		},

		// Line Continuations
		{
			name:  "basic line continuation",
			input: "RUN echo hello \\\nworld\n",
			want: []item{
				makeItemRun(),
				makeItemSpace(),
				makeItemAtom("echo"),
				makeItemSpace(),
				makeItemAtom("hello"),
				makeItemSpace(),
				makeItemAtom("world"),
				makeItemNL(),
				makeItemEOF(),
			},
		},
		{
			name:  "line continuation with spaces and comments",
			input: "RUN echo hello \\  # comment\n   world\n",
			want: []item{
				makeItemRun(),
				makeItemSpace(),
				makeItemAtom("echo"),
				makeItemSpace(),
				makeItemAtom("hello"),
				makeItemSpace(),
				makeItemAtom("world"),
				makeItemNL(),
				makeItemEOF(),
			},
		},
		{
			name:  "line continuation inside double quotes",
			input: "RUN echo \"hello \\\nworld\"\n",
			want: []item{
				makeItemRun(),
				makeItemSpace(),
				makeItemAtom("echo"),
				makeItemSpace(),
				makeItemAtom("\"hello \\\nworld\""),
				makeItemNL(),
				makeItemEOF(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lexer := lex("test", tt.input)

			var got []item

			for {
				itm := lexer.nextItem()

				got = append(got, itm)

				if itm.Typ == itemEOF || itm.Typ == itemError {
					break
				}
			}

			if len(got) != len(tt.want) {
				t.Fatalf("got %d items, want %d: got %v", len(got), len(tt.want), got)
			}

			for i, wantItem := range tt.want {
				gotItem := got[i]
				if gotItem.Typ != wantItem.Typ || gotItem.Val != wantItem.Val {
					t.Errorf("item mismatch at index %d: got {Type:%d Val:%q}, want {Type:%d Val:%q}",
						i, gotItem.Typ, gotItem.Val, wantItem.Typ, wantItem.Val)
				}
			}
		})
	}
}

func makeItemEnv() item {
	return item{Typ: itemEnv, Val: string(CmdEnv)}
}

func makeItemArg() item {
	return item{Typ: itemArg, Val: string(CmdArg)}
}

func makeItemSet() item {
	return item{Typ: itemSet, Val: string(CmdSet)}
}

func makeItemLet() item {
	return item{Typ: itemLet, Val: string(CmdLet)}
}

func makeItemSpace(n ...int) item {
	count := 1
	if len(n) > 0 {
		count = n[0]
	}

	return item{Typ: itemWS, Val: strings.Repeat(" ", count)}
}

func makeItemAtom(val string) item {
	return item{Typ: itemAtom, Val: val}
}

func makeItemNL() item {
	return item{Typ: itemNL, Val: "\n"}
}

func makeItemError(val string) item {
	return item{Typ: itemError, Val: val}
}

func makeItemRun() item {
	return item{Typ: itemRun, Val: string(CmdRun)}
}

func makeItemEOF() item {
	return item{Typ: itemEOF, Val: ""}
}
