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
			name:  "arg with flag containing embedded hash",
			input: "ARG --description=issue#765 KEY=VALUE\n",
			want: []item{
				makeItemArg(),
				makeItemSpace(),
				makeItemAtom("--description=issue#765"),
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
		// Heredocs
		{
			name:  "run with basic heredoc",
			input: "RUN <<EOF\nset -e\napk update\nEOF\n",
			want: []item{
				makeItemRun(),
				makeItemSpace(),
				makeItemAtom("<<EOF"),
				makeItemNL(),
				makeItemHeredoc("set -e\napk update\n"),
				makeItemEOF(),
			},
		},
		{
			name:  "run with tab chomping heredoc",
			input: "RUN <<-EOF\n\tset -e\n\techo test\n\tEOF\n",
			want: []item{
				makeItemRun(),
				makeItemSpace(),
				makeItemAtom("<<-EOF"),
				makeItemNL(),
				makeItemHeredoc("set -e\necho test\n"),
				makeItemEOF(),
			},
		},
		{
			name:  "run with custom interpreter and heredoc",
			input: "RUN python3 <<EOF\nimport json\nEOF\n",
			want: []item{
				makeItemRun(),
				makeItemSpace(),
				makeItemAtom("python3"),
				makeItemSpace(),
				makeItemAtom("<<EOF"),
				makeItemNL(),
				makeItemHeredoc("import json\n"),
				makeItemEOF(),
			},
		},
		{
			name:  "copy with heredoc",
			input: "COPY <<EOF /dest/config.json\n{\"ok\": true}\nEOF\n",
			want: []item{
				makeItemCopy(),
				makeItemSpace(),
				makeItemAtom("<<EOF"),
				makeItemSpace(),
				makeItemAtom("/dest/config.json"),
				makeItemNL(),
				makeItemHeredoc("{\"ok\": true}\n"),
				makeItemEOF(),
			},
		},
		{
			name:  "copy with multiple heredocs",
			input: "COPY <<EOF1 <<EOF2 /dest\nfile 1\nEOF1\nfile 2\nEOF2\n",
			want: []item{
				makeItemCopy(),
				makeItemSpace(),
				makeItemAtom("<<EOF1"),
				makeItemSpace(),
				makeItemAtom("<<EOF2"),
				makeItemSpace(),
				makeItemAtom("/dest"),
				makeItemNL(),
				makeItemHeredoc("file 1\n"),
				makeItemHeredoc("file 2\n"),
				makeItemEOF(),
			},
		},
		{
			name:  "unterminated heredoc error",
			input: "RUN <<EOF\nhello\n",
			want: []item{
				makeItemRun(),
				makeItemSpace(),
				makeItemAtom("<<EOF"),
				makeItemNL(),
				makeItemError("unterminated heredoc \"EOF\" (opened at line 1)"),
			},
		},
		{
			name:  "unterminated heredoc at EOF without newline",
			input: "RUN <<EOF",
			want: []item{
				makeItemRun(),
				makeItemSpace(),
				makeItemAtom("<<EOF"),
				makeItemError("unterminated heredoc \"EOF\" (opened at line 1)"),
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

func makeItemCopy() item {
	return item{Typ: itemCopy, Val: string(CmdCopy)}
}

func makeItemHeredoc(val string) item {
	return item{Typ: itemHeredoc, Val: val}
}

func makeItemEOF() item {
	return item{Typ: itemEOF, Val: ""}
}

const (
	testEchoLines  = "echo hello\necho world\n"
	testHeredocEOF = "EOF"
	testDeclEOF    = "<<EOF"
)

func TestChompHeredocContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no tabs",
			input: testEchoLines,
			want:  testEchoLines,
		},
		{
			name:  "single leading tab",
			input: "\techo hello\n\techo world\n",
			want:  testEchoLines,
		},
		{
			name:  "multiple leading tabs",
			input: "\t\tline 1\n\t\t\tline 2\n",
			want:  "line 1\nline 2\n",
		},
		{
			name:  "leading spaces not stripped",
			input: "  \tindented\n",
			want:  "  \tindented\n",
		},
		{
			name:  "empty lines and tabs only",
			input: "\t\t\n\t\ntext\n",
			want:  "\n\ntext\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ChompHeredocContent(tt.input)
			if got != tt.want {
				t.Errorf("ChompHeredocContent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseHeredocDecl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src       string
		wantName  string
		wantChomp bool
		wantExp   bool
		wantOk    bool
	}{
		{src: testDeclEOF, wantName: testHeredocEOF, wantChomp: false, wantExp: true, wantOk: true},
		{src: "<<-EOF", wantName: testHeredocEOF, wantChomp: true, wantExp: true, wantOk: true},
		{src: "<<'EOF'", wantName: testHeredocEOF, wantChomp: false, wantExp: false, wantOk: true},
		{src: "<<\"EOF\"", wantName: testHeredocEOF, wantChomp: false, wantExp: false, wantOk: true},
		{src: "<<-'EOF'", wantName: testHeredocEOF, wantChomp: true, wantExp: false, wantOk: true},
		{src: "0" + testDeclEOF, wantOk: false},
		{src: "1<<2", wantOk: false},
		{src: "<<", wantOk: false},
		{src: "<<-", wantOk: false},
		{src: "<<''", wantOk: false},
		{src: "<<'EOF\"", wantOk: false},
		{src: "a<<EOF", wantOk: false},
		{src: "regular_word", wantOk: false},
		{src: "<<<\"x\"", wantOk: false},
		{src: "'<<foo'", wantOk: false},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			t.Parallel()

			decl, ok := ParseHeredocDecl(tt.src)
			if ok != tt.wantOk {
				t.Fatalf("ParseHeredocDecl(%q) ok = %v, want %v", tt.src, ok, tt.wantOk)
			}

			if !ok {
				return
			}

			if decl.Name != tt.wantName {
				t.Errorf("ParseHeredocDecl(%q).Name = %q, want %q", tt.src, decl.Name, tt.wantName)
			}

			if decl.Chomp != tt.wantChomp {
				t.Errorf("ParseHeredocDecl(%q).Chomp = %v, want %v", tt.src, decl.Chomp, tt.wantChomp)
			}

			if decl.Expand != tt.wantExp {
				t.Errorf("ParseHeredocDecl(%q).Expand = %v, want %v", tt.src, decl.Expand, tt.wantExp)
			}
		})
	}
}
