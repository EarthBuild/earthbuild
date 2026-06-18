package earthfile

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

type namedStringReader struct {
	*strings.Reader
}

func (n *namedStringReader) Name() string {
	return "Earthfile"
}

var _ NamedReader = &namedStringReader{}

func TestParseOpts(t *testing.T) {
	t.Parallel()

	//nolint:goconst
	tests := []struct {
		check     func(*require.Assertions, Earthfile, error)
		note      string
		earthfile string
	}{
		{
			note: "it parses SET commands",
			earthfile: `
VERSION 0.7

foo:
    LET foo = bar
    SET foo = baz
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				foo := s.Targets[0]
				r.Len(foo.Recipe, 2)
				set := foo.Recipe[1]
				r.NotNil(set.Command)
				r.Equal("SET", set.Command.Name)
				r.Equal([]string{"foo", "=", "baz"}, set.Command.Args)
			},
		},
		{
			note: "it parses LET commands",
			earthfile: `
VERSION 0.7

LET foo = bar

foo:
    LET bacon = eggs
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.BaseRecipe, 1)
				global := s.BaseRecipe[0]
				r.NotNil(global.Command)
				r.Equal("LET", global.Command.Name)
				r.Equal([]string{"foo", "=", "bar"}, global.Command.Args)
				r.Len(s.Targets, 1)
				foo := s.Targets[0]
				r.Len(foo.Recipe, 1)
				let := foo.Recipe[0]
				r.NotNil(let.Command)
				r.Equal("LET", let.Command.Name)
				r.Equal([]string{"bacon", "=", "eggs"}, let.Command.Args)
			},
		},
		{
			note: "it safely ignores comments outside of documentation",
			earthfile: `
# this is an early comment.

# VERSION does not get documentation.
VERSION 0.6 # Trailing comments do not cause parsing errors at the top level
WORKDIR /tmp

# a comment before an IF or a FOR does not cause parser errors
IF foo
    RUN echo foo
END

bar:

baz:
    # comments in an otherwise empty target should be
    # ignored.

# foo - Comments between targets should not be parsed as
# documentation, even if they start with the target's name.

foo: # inline  comments do not consume newlines
    # RUN does not get documentation.
    RUN echo foo

    ARG foo=bar # inline comments should also be ignored.

    # Lonely comment blocks in
    # targets should be ignored.

    # Even if they don't have a trailing newline.`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 3)
				foo := s.Targets[2]
				r.Equal("foo", foo.Name)
				r.Empty(foo.Docs)
			},
		},
		{
			note: "targets with leading whitespace cause errors",
			earthfile: `
VERSION 0.6

  foo:
    RUN echo foo
`,
			check: func(r *require.Assertions, _ Earthfile, err error) {
				r.Error(err)
				r.ErrorContains(err, "must start at the beginning of the line")
			},
		},
		{
			note: "it parses a basic target",
			earthfile: `
VERSION 0.6

foo:
    RUN echo foo
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Version.Args, 1)
				r.Equal("0.6", s.Version.Args[0])
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Equal("foo", target.Name)
				r.Len(target.Recipe, 1)
				recipe := target.Recipe[0]
				r.NotNil(recipe.Command)
				r.Equal("RUN", recipe.Command.Name)
				r.Equal([]string{"echo", "foo"}, recipe.Command.Args)
			},
		},
		{
			note: "nested quotes inside of shellouts do not break parent quotes",
			earthfile: `
VERSION 0.6

foo:
    RUN echo "$(echo "foo     bar")"
    ENV FOO="$(echo "foo     bar")"
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Equal("foo", target.Name)
				r.Len(target.Recipe, 2)
				run := target.Recipe[0]
				r.NotNil(run.Command)
				r.Equal("RUN", run.Command.Name)
				r.Equal([]string{"echo", `"$(echo "foo     bar")"`}, run.Command.Args)

				env := target.Recipe[1]
				r.Equal("ENV", env.Command.Name)
				r.Equal([]string{"FOO", "=", `"$(echo "foo     bar")"`}, env.Command.Args)
			},
		},
		{
			note: "nested shellouts inside of shellouts do not break parent shellouts",
			earthfile: `
VERSION 0.6

foo:
    RUN echo $(echo $(echo -n foo) $(echo -n bar))
    ENV FOO=$(echo $(echo -n foo) $(echo -n bar))
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Equal("foo", target.Name)
				r.Len(target.Recipe, 2)
				run := target.Recipe[0]
				r.NotNil(run.Command)
				r.Equal("RUN", run.Command.Name)
				r.Equal([]string{"echo", "$(echo $(echo -n foo) $(echo -n bar))"}, run.Command.Args)

				env := target.Recipe[1]
				r.Equal("ENV", env.Command.Name)
				r.Equal([]string{"FOO", "=", "$(echo $(echo -n foo) $(echo -n bar))"}, env.Command.Args)
			},
		},
		{
			note: "nested parens inside of quotes do not break parent shellouts",
			earthfile: `
VERSION 0.6

foo:
    ARG foo = "$(echo "()")"
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Equal("foo", target.Name)
				r.Len(target.Recipe, 1)
				run := target.Recipe[0]
				r.NotNil(run.Command)
				r.Equal("ARG", run.Command.Name)
				r.Equal([]string{"foo", "=", `"$(echo "()")"`}, run.Command.Args)
			},
		},
		{
			note: "ENV and ARG values retain inner whitespace",
			earthfile: `
VERSION 0.6

foo:
    ARG foo = $ ( foo )
    ENV foo = $ ( foo )
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Equal("foo", target.Name)
				r.Len(target.Recipe, 2)
				arg := target.Recipe[0]
				r.NotNil(arg.Command)
				r.Equal("ARG", arg.Command.Name)
				r.Equal([]string{"foo", "=", "$ ( foo )"}, arg.Command.Args)

				env := target.Recipe[1]
				r.Equal("ENV", env.Command.Name)
				r.Equal([]string{"foo", "=", "$ ( foo )"}, env.Command.Args)
			},
		},
		{
			note: "it successfully parses unindented comments mid-recipe",
			earthfile: `
VERSION 0.7

foo:
    RUN some_command
# Comment regarding something
    SAVE ARTIFACT /stuff
`,
			check: func(r *require.Assertions, _ Earthfile, err error) {
				r.NoError(err)
			},
		},
		{
			note: "it parses target documentation",
			earthfile: `
VERSION 0.6

# foo echoes 'foo'
foo:
    RUN echo foo
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Equal("foo", target.Name)
				r.Equal("foo echoes 'foo'\n", target.Docs)
			},
		},
		{
			note: "it respects leading whitespace in documentation",
			earthfile: `
VERSION 0.7

# foo outputs formatted JSON
#
# Sample output:
#
#     $ earth +foo --json='{"a":"b","c":"d"}'
#     {
#         "a": "b",
#         "c": "d"
#     }
foo:
    ARG json
    RUN echo $json | jq .
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Equal("foo", target.Name)
				r.Equal(`foo outputs formatted JSON

Sample output:

    $ earth +foo --json='{"a":"b","c":"d"}'
    {
        "a": "b",
        "c": "d"
    }
`, target.Docs)
			},
		},
		{
			note: "it parses documentation on later targets",
			earthfile: `
VERSION 0.6

bar:
    RUN echo bar

# foo echoes 'foo'
foo:
    RUN echo foo
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 2)
				target := s.Targets[1]
				r.Equal("foo", target.Name)
				r.Equal("foo echoes 'foo'\n", target.Docs)
			},
		},
		{
			note: "it parses multiline documentation",
			earthfile: `
VERSION 0.6

# foo echoes 'foo'
#
# and that's all.
foo:
    RUN echo foo
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Equal("foo", target.Name)
				r.Equal("foo echoes 'foo'\n\nand that's all.\n", target.Docs)
			},
		},
		{
			note: "it does not parse comments with empty lines after them as documentation",
			earthfile: `
VERSION 0.6

# foo echoes 'foo'

foo:
    RUN echo foo
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Equal("foo", target.Name)
				r.Empty(target.Docs)
			},
		},
		{
			note: "it does not check the comment against the target name",
			earthfile: `
VERSION 0.6

# echoes 'foo'
foo:
    RUN echo foo
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Equal("foo", target.Name)
				r.Equal("echoes 'foo'\n", target.Docs)
			},
		},
		{
			note: "it skips comments that have different indentation",
			earthfile: `
VERSION 0.6

foo:
    RUN echo foo
    # this is a trailing comment in foo
# bar is a documented target
bar:
    RUN echo bar
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 2)
				target := s.Targets[1]
				r.Equal("bar", target.Name)
				r.Equal("bar is a documented target\n", target.Docs)
			},
		},
		{
			note: "it does not treat comments in otherwise-empty targets as documentation for the next target",
			earthfile: `
VERSION 0.7


foo:
    # bar is not a documentation line

bar:
    RUN echo bar
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 2)
				target := s.Targets[1]
				r.Equal("bar", target.Name)
				r.Empty(target.Docs)
			},
		},
		{
			note: "it parses documentation on ARGs",
			earthfile: `
VERSION 0.6

foo:
    # foo is the argument that will be echoed
    ARG foo = bar
    RUN echo $foo
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Len(target.Recipe, 2)
				arg := target.Recipe[0]
				r.NotNil(arg.Command)
				r.Equal("ARG", arg.Command.Name)
				r.Equal("foo is the argument that will be echoed\n", arg.Command.Docs)
			},
		},
		{
			note: "it parses multiline documentation on global ARGs",
			earthfile: `
VERSION 0.7
FROM alpine:3.18

# globalArg is a documented global arg
# with multiple lines.
ARG --global globalArg
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.BaseRecipe, 2)
				arg := s.BaseRecipe[1]
				r.NotNil(arg.Command)
				r.Equal("ARG", arg.Command.Name)
				r.Equal("globalArg is a documented global arg\nwith multiple lines.\n", arg.Command.Docs)
			},
		},
		{
			note: "it parses documentation on SAVE ARTIFACT",
			earthfile: `
VERSION 0.6

foo:
    RUN echo foo > bar.txt
    # bar.txt will contain the output of this target
    SAVE ARTIFACT bar.txt
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Len(target.Recipe, 2)
				arg := target.Recipe[1]
				r.NotNil(arg.Command)
				r.Equal("SAVE ARTIFACT", arg.Command.Name)
				r.Equal("bar.txt will contain the output of this target\n", arg.Command.Docs)
			},
		},
		{
			note: "it parses documentation on SAVE IMAGE",
			earthfile: `
VERSION 0.6

foo:
    RUN echo foo > bar.txt
    # foo is an image that contains a bar.txt file
    SAVE IMAGE foo
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Len(target.Recipe, 2)
				arg := target.Recipe[1]
				r.NotNil(arg.Command)
				r.Equal("SAVE IMAGE", arg.Command.Name)
				r.Equal("foo is an image that contains a bar.txt file\n", arg.Command.Docs)
			},
		},
		{
			note: "complex character sequences in single quotes",
			earthfile: `VERSION 0.8

target:
  RUN find . -type f -iname '*.md' | xargs -n 1 sed -i 's/{[^}]*}//g'
  RUN find . -type f -iname '*.md' | xargs vale --config /etc/vale/vale.ini --output line --minAlertLevel error
`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				r.Len(s.Targets[0].Recipe, 2)
			},
		},
		{
			note: "regression test for single-quoted #",
			earthfile: `VERSION 0.8

test:
    FROM debian:9
    RUN set -x \
     && sed -i \
            -e 's, universe multiverse, universe # multiverse,' \
            /etc/apt/sources.list
    SAVE IMAGE --push blah`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Len(target.Recipe, 3)
				// Confirm that the single-quoted string is intact
				r.Contains(target.Recipe[1].Command.Args, `'s, universe multiverse, universe # multiverse,'`)
			},
		},
		{
			note: "regression test for escaped # in $()",
			earthfile: `VERSION 0.8

thebug:
    FROM alpine:3.24.0
    ARG myarg=$(echo "a#b#c" | cut -f2 -d\#)
    RUN touch /some-file
    RUN echo "myarg is \"$myarg\""
    RUN test -f /some-file`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Len(target.Recipe, 5)
				// Confirm that the escaped expression is intact
				r.Contains(target.Recipe[1].Command.Args, `$(echo "a#b#c" | cut -f2 -d\#)`)
			},
		},
		{
			note: "regression test for single-quoted string in shell expression",
			earthfile: `VERSION 0.8
FROM alpine:3.24.0

arg-plain:
    ARG val=$(echo run | tr -d '"')
    RUN echo $val`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Len(target.Recipe, 2)
				// Confirm that the single-quoted string is intact
				r.Contains(target.Recipe[0].Command.Args, `$(echo run | tr -d '"')`)
			},
		},
		{
			note: "regression test for single-quoted string in RUN",
			earthfile: `VERSION 0.8
FROM alpine:3.24.0

run-plain:
    RUN echo run | tr -d '"'`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Len(target.Recipe, 1)
			},
		},
		{
			note: "regression test for escaped double-quoted strings in shell expression",
			earthfile: `VERSION 0.8
FROM alpine:3.24.0

arg-esc:
    ARG val=$(echo single | tr -d "\"")
    RUN echo $val`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Len(target.Recipe, 2)
				// Confirm that the single-quoted string is intact
				r.Contains(target.Recipe[0].Command.Args, `$(echo single | tr -d "\"")`)
			},
		},
		{
			note: "regression test for escaped \\ & double-quotes in shell expression",
			earthfile: `VERSION 0.8
FROM alpine:3.24.0

arg-esc:
    ARG val=$(echo single | tr -d "\\\"")
    RUN echo $val`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Len(target.Recipe, 2)
				// Confirm that the single-quoted string is intact
				r.Contains(target.Recipe[0].Command.Args, `$(echo single | tr -d "\\\"")`)
			},
		},
		{
			note: "regression test for single-quoted commands",
			earthfile: `VERSION 0.8

FROM alpine:3.24.0

test:
  RUN 'echo "message'
  RUN 'echo "message"'`,
			check: func(r *require.Assertions, s Earthfile, err error) {
				r.NoError(err)
				r.Len(s.Targets, 1)
				target := s.Targets[0]
				r.Len(target.Recipe, 2)
				// Confirm that the single-quoted strings are intact
				r.Contains(target.Recipe[0].Command.Args, `'echo "message'`)
				r.Contains(target.Recipe[1].Command.Args, `'echo "message"'`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.note, func(t *testing.T) {
			t.Parallel()

			r := namedStringReader{strings.NewReader(test.earthfile)}
			s, err := ParseOpts(FromReader(&r))
			test.check(require.New(t), s, err)
		})
	}
}

//nolint:goconst
func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  Earthfile
	}{
		{
			name: "basic target",
			input: `VERSION 0.8
build:
  FROM alpine:3.18
  RUN echo hello
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "build",
						Recipe: Block{
							{
								Command: &Command{
									Name: "FROM",
									Args: []string{"alpine:3.18"},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"echo", "hello"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "if block",
			input: `VERSION 0.8
build:
  IF [ "$VAR" = "1" ]
    RUN echo "yes"
  ELSE IF [ "$VAR" = "2" ]
    RUN echo "no"
  ELSE
    RUN echo "maybe"
  END
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "build",
						Recipe: Block{
							{
								If: &IfStatement{
									Expression: []string{"[", "\"$VAR\"", "=", "\"1\"", "]"},
									IfBody: Block{
										{
											Command: &Command{
												Name: "RUN",
												Args: []string{"echo", "\"yes\""},
											},
										},
									},
									ElseIf: []ElseIf{
										{
											Expression: []string{"[", "\"$VAR\"", "=", "\"2\"", "]"},
											Body: Block{
												{
													Command: &Command{
														Name: "RUN",
														Args: []string{"echo", "\"no\""},
													},
												},
											},
										},
									},
									ElseBody: &Block{
										{
											Command: &Command{
												Name: "RUN",
												Args: []string{"echo", "\"maybe\""},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "for block",
			input: `VERSION 0.8
build:
  FOR arg IN foo bar
    RUN echo $arg
  END
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "build",
						Recipe: Block{
							{
								For: &ForStatement{
									Args: []string{"arg", "IN", "foo", "bar"},
									Body: Block{
										{
											Command: &Command{
												Name: "RUN",
												Args: []string{"echo", "$arg"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "try block",
			input: `VERSION 0.8
build:
  TRY
    RUN echo "try"
  CATCH
    RUN echo "catch"
  FINALLY
    RUN echo "finally"
  END
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "build",
						Recipe: Block{
							{
								Try: &TryStatement{
									TryBody: Block{
										{
											Command: &Command{
												Name: "RUN",
												Args: []string{"echo", "\"try\""},
											},
										},
									},
									CatchBody: &Block{
										{
											Command: &Command{
												Name: "RUN",
												Args: []string{"echo", "\"catch\""},
											},
										},
									},
									FinallyBody: &Block{
										{
											Command: &Command{
												Name: "RUN",
												Args: []string{"echo", "\"finally\""},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "with block",
			input: `VERSION 0.8
build:
  WITH DOCKER --pull alpine:3.18
    RUN echo "with"
  END
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "build",
						Recipe: Block{
							{
								With: &WithStatement{
									Command: Command{
										Name: "DOCKER",
										Args: []string{"--pull", "alpine:3.18"},
									},
									Body: Block{
										{
											Command: &Command{
												Name: "RUN",
												Args: []string{"echo", "\"with\""},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "wait block",
			input: `VERSION 0.8
build:
  WAIT
    RUN echo "wait"
  END
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "build",
						Recipe: Block{
							{
								Wait: &WaitStatement{
									Body: Block{
										{
											Command: &Command{
												Name: "RUN",
												Args: []string{"echo", "\"wait\""},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "user command and function definitions",
			input: `VERSION 0.8
COMMAND my-command
  FROM alpine:3.18
  RUN echo "cmd"

FUNCTION my-func
  FROM alpine:3.18
  RUN echo "func"
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Functions: []Function{
					{
						Name: "COMMAND my-command",
						Recipe: Block{
							{
								Command: &Command{
									Name: "FROM",
									Args: []string{"alpine:3.18"},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"echo", "\"cmd\""},
								},
							},
						},
					},
					{
						Name: "FUNCTION my-func",
						Recipe: Block{
							{
								Command: &Command{
									Name: "FROM",
									Args: []string{"alpine:3.18"},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"echo", "\"func\""},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "line continuation and comments",
			input: `VERSION 0.8
# this is a comment
build:
  RUN echo "hello \
    world $(echo 'nested') and $VAR" # inline comment
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "build",
						Docs: "this is a comment\n",
						Recipe: Block{
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"echo", "\"hello \\\n    world $(echo 'nested') and $VAR\""},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "it parses SET commands",
			input: `VERSION 0.7
foo:
  LET foo = bar
  SET foo = baz
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.7"},
				},
				Targets: []Target{
					{
						Name: "foo",
						Recipe: Block{
							{
								Command: &Command{
									Name: "LET",
									Args: []string{"foo", "=", "bar"},
								},
							},
							{
								Command: &Command{
									Name: "SET",
									Args: []string{"foo", "=", "baz"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "it parses LET commands",
			input: `VERSION 0.7
LET foo = bar
foo:
  LET bacon = eggs
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.7"},
				},
				BaseRecipe: Block{
					{
						Command: &Command{
							Name: "LET",
							Args: []string{"foo", "=", "bar"},
						},
					},
				},
				Targets: []Target{
					{
						Name: "foo",
						Recipe: Block{
							{
								Command: &Command{
									Name: "LET",
									Args: []string{"bacon", "=", "eggs"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "nested quotes inside of shellouts",
			input: `VERSION 0.6
foo:
  RUN echo "$(echo "foo     bar")"
  ENV FOO="$(echo "foo     bar")"
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.6"},
				},
				Targets: []Target{
					{
						Name: "foo",
						Recipe: Block{
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"echo", `"$(echo "foo     bar")"`},
								},
							},
							{
								Command: &Command{
									Name: "ENV",
									Args: []string{"FOO", "=", `"$(echo "foo     bar")"`},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "nested shellouts inside of shellouts",
			input: `VERSION 0.6
foo:
  RUN echo $(echo $(echo -n foo) $(echo -n bar))
  ENV FOO=$(echo $(echo -n foo) $(echo -n bar))
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.6"},
				},
				Targets: []Target{
					{
						Name: "foo",
						Recipe: Block{
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"echo", "$(echo $(echo -n foo) $(echo -n bar))"},
								},
							},
							{
								Command: &Command{
									Name: "ENV",
									Args: []string{"FOO", "=", "$(echo $(echo -n foo) $(echo -n bar))"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "it parses documentation on ARGs",
			input: `VERSION 0.6
foo:
  # foo is the argument that will be echoed
  ARG foo = bar
  RUN echo $foo
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.6"},
				},
				Targets: []Target{
					{
						Name: "foo",
						Recipe: Block{
							{
								Command: &Command{
									Name: "ARG",
									Docs: "foo is the argument that will be echoed\n",
									Args: []string{"foo", "=", "bar"},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"echo", "$foo"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "it parses multiline documentation on global ARGs",
			input: `VERSION 0.7
FROM alpine:3.18
# globalArg is a documented global arg
# with multiple lines.
ARG --global globalArg
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.7"},
				},
				BaseRecipe: Block{
					{
						Command: &Command{
							Name: "FROM",
							Args: []string{"alpine:3.18"},
						},
					},
					{
						Command: &Command{
							Name: "ARG",
							Docs: "globalArg is a documented global arg\nwith multiple lines.\n",
							Args: []string{"--global", "globalArg"},
						},
					},
				},
			},
		},
		{
			name: "complex character sequences in single quotes",
			input: `VERSION 0.8
target:
  RUN find . -type f -iname '*.md' | xargs -n 1 sed -i 's/{[^}]*}//g'
  RUN find . -type f -iname '*.md' | xargs vale --config /etc/vale/vale.ini --output line --minAlertLevel error
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "target",
						Recipe: Block{
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{
										"find", ".", "-type", "f", "-iname", "'*.md'",
										"|", "xargs", "-n", "1", "sed", "-i", "'s/{[^}]*}//g'",
									},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{
										"find", ".", "-type", "f", "-iname", "'*.md'", "|", "xargs", "vale",
										"--config", "/etc/vale/vale.ini", "--output", "line",
										"--minAlertLevel", "error",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "regression test for single-quoted #",
			input: `VERSION 0.8
test:
  FROM debian:9
  RUN set -x \
   && sed -i \
          -e 's, universe multiverse, universe # multiverse,' \
          /etc/apt/sources.list
  SAVE IMAGE --push blah
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "test",
						Recipe: Block{
							{
								Command: &Command{
									Name: "FROM",
									Args: []string{"debian:9"},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{
										"set", "-x", "&&", "sed", "-i", "-e",
										"'s, universe multiverse, universe # multiverse,'",
										"/etc/apt/sources.list",
									},
								},
							},
							{
								Command: &Command{
									Name: "SAVE IMAGE",
									Args: []string{"--push", "blah"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "regression test for escaped # in $()",
			input: `VERSION 0.8
thebug:
  FROM alpine:3.24.0
  ARG myarg=$(echo "a#b#c" | cut -f2 -d\#)
  RUN touch /some-file
  RUN echo "myarg is \"$myarg\""
  RUN test -f /some-file
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "thebug",
						Recipe: Block{
							{
								Command: &Command{
									Name: "FROM",
									Args: []string{"alpine:3.24.0"},
								},
							},
							{
								Command: &Command{
									Name: "ARG",
									Args: []string{"myarg", "=", `$(echo "a#b#c" | cut -f2 -d\#)`},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"touch", "/some-file"},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"echo", `"myarg is \"$myarg\""`},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"test", "-f", "/some-file"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "ARG with value containing equals sign",
			input: `VERSION 0.8
test:
  ARG MY_URL=https://example.com?foo=bar
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "test",
						Recipe: Block{
							{
								Command: &Command{
									Name: "ARG",
									Args: []string{"MY_URL", "=", "https://example.com?foo=bar"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "ARG with value starting with no spaces and containing spaces",
			input: `VERSION 0.8
test:
  ARG BUILD_TAGS=dfrunmount dfrunsecurity dfsecrets dfssh
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "test",
						Recipe: Block{
							{
								Command: &Command{
									Name: "ARG",
									Args: []string{"BUILD_TAGS", "=", "dfrunmount dfrunsecurity dfsecrets dfssh"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "regression test for single-quoted commands",
			input: `VERSION 0.8
FROM alpine:3.24.0
test:
  RUN 'echo "message'
  RUN 'echo "message"'
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				BaseRecipe: Block{
					{
						Command: &Command{
							Name: "FROM",
							Args: []string{"alpine:3.24.0"},
						},
					},
				},
				Targets: []Target{
					{
						Name: "test",
						Recipe: Block{
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"'echo \"message'"},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"'echo \"message\"'"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "empty target followed by function definition",
			input: `VERSION 0.8
build:
FUNCTION my-func
  FROM alpine:3.18
  RUN echo "func"
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name:   "build",
						Recipe: nil,
					},
				},
				Functions: []Function{
					{
						Name: "FUNCTION my-func",
						Recipe: Block{
							{
								Command: &Command{
									Name: "FROM",
									Args: []string{"alpine:3.18"},
								},
							},
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"echo", "\"func\""},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "escaped quotes and line continuations",
			input: `VERSION 0.8
test:
  RUN echo \"\
    hello
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "test",
						Recipe: Block{
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"echo", `\"hello`},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "nested WITH inside IF",
			input: `VERSION 0.8
build:
  IF [ "$VAR" = "1" ]
    WITH DOCKER --pull alpine:3.18
      RUN echo "yes"
    END
  END
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "build",
						Recipe: Block{
							{
								If: &IfStatement{
									Expression: []string{"[", "\"$VAR\"", "=", "\"1\"", "]"},
									IfBody: Block{
										{
											With: &WithStatement{
												Command: Command{
													Name: "DOCKER",
													Args: []string{"--pull", "alpine:3.18"},
												},
												Body: Block{
													{
														Command: &Command{
															Name: "RUN",
															Args: []string{"echo", "\"yes\""},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "hash sign in middle of parameter expansion",
			input: `VERSION 0.8
build:
  RUN GOARM=${VARIANT#v} go build -o main main.go
`,
			want: Earthfile{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "build",
						Recipe: Block{
							{
								Command: &Command{
									Name: "RUN",
									Args: []string{"GOARM=${VARIANT#v}", "go", "build", "-o", "main", "main.go"},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			actual, err := Parse("Earthfile", tc.input)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			zeroSourceLocations(&actual)
			zeroSourceLocations(&tc.want)

			if diff := cmp.Diff(tc.want, actual); diff != "" {
				t.Errorf("AST mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func zeroSourceLocations(ef *Earthfile) {
	ef.SourceLocation = nil
	if ef.Version != nil {
		ef.Version.SourceLocation = nil
	}

	for i := range ef.Targets {
		ef.Targets[i].SourceLocation = nil
		zeroBlockSourceLocations(ef.Targets[i].Recipe)
	}

	for i := range ef.Functions {
		ef.Functions[i].SourceLocation = nil
		zeroBlockSourceLocations(ef.Functions[i].Recipe)
	}

	zeroBlockSourceLocations(ef.BaseRecipe)
}

func zeroBlockSourceLocations(block Block) {
	for i := range block {
		block[i].SourceLocation = nil
		if block[i].Command != nil {
			block[i].Command.SourceLocation = nil
		}

		if block[i].If != nil {
			block[i].If.SourceLocation = nil
			zeroBlockSourceLocations(block[i].If.IfBody)

			for j := range block[i].If.ElseIf {
				block[i].If.ElseIf[j].SourceLocation = nil
				zeroBlockSourceLocations(block[i].If.ElseIf[j].Body)
			}

			if block[i].If.ElseBody != nil {
				zeroBlockSourceLocations(*block[i].If.ElseBody)
			}
		}

		if block[i].For != nil {
			block[i].For.SourceLocation = nil
			zeroBlockSourceLocations(block[i].For.Body)
		}

		if block[i].Try != nil {
			block[i].Try.SourceLocation = nil
			zeroBlockSourceLocations(block[i].Try.TryBody)

			if block[i].Try.CatchBody != nil {
				zeroBlockSourceLocations(*block[i].Try.CatchBody)
			}

			if block[i].Try.FinallyBody != nil {
				zeroBlockSourceLocations(*block[i].Try.FinallyBody)
			}
		}

		if block[i].With != nil {
			block[i].With.SourceLocation = nil
			block[i].With.Command.SourceLocation = nil
			zeroBlockSourceLocations(block[i].With.Body)
		}

		if block[i].Wait != nil {
			block[i].Wait.SourceLocation = nil
			zeroBlockSourceLocations(block[i].Wait.Body)
		}
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantError string
	}{
		{
			name: "if block missing END",
			input: `VERSION 0.8
build:
  IF [ "$VAR" = "1" ]
    RUN echo "yes"
`,
			wantError: "expected END to close IF statement",
		},
		{
			name: "for block missing END",
			input: `VERSION 0.8
build:
  FOR arg IN foo bar
    RUN echo $arg
`,
			wantError: "expected END to close FOR statement",
		},
		{
			name: "try block missing END",
			input: `VERSION 0.8
build:
  TRY
    RUN echo "try"
`,
			wantError: "expected END to close TRY statement",
		},
		{
			name: "with block missing END",
			input: `VERSION 0.8
build:
  WITH DOCKER --pull alpine:3.18
    RUN echo "with"
`,
			wantError: "expected END to close WITH statement",
		},
		{
			name: "wait block missing END",
			input: `VERSION 0.8
build:
  WAIT
    RUN echo "wait"
`,
			wantError: "expected END to close WAIT statement",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := Parse("Earthfile", tc.input)
			if err == nil {
				t.Fatalf("expected parse error, got nil")
			}

			if !strings.Contains(err.Error(), tc.wantError) {
				t.Errorf("expected error containing %q, got %q", tc.wantError, err.Error())
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	f.Add(`VERSION 0.8
FROM alpine:latest
RUN echo hello
`)

	f.Add(`VERSION 0.8
target:
  FROM DOCKERFILE .
  RUN echo 123
  SAVE ARTIFACT ./file AS LOCAL ./dest
  SAVE IMAGE img:latest
  GIT CLONE repo dest
`)

	f.Add(`VERSION 0.8
target:
  IF [ "$foo" = "bar" ]
    RUN echo bar
  ELSE IF [ "$foo" = "baz" ]
    RUN echo baz
  ELSE
    RUN echo else
  END
  FOR x IN a b c
    RUN echo $x
  END
  TRY
    RUN command
  CATCH
    RUN catch
  FINALLY
    RUN finally
  END
`)

	f.Add(`# Standalone comment
VERSION 0.8
ARG --global global_arg = 1

FUNCTION my-func:
  ARG --required func_arg
  RUN echo $func_arg

target:
  DO +my-func --func_arg=val # EOL comment
`)

	f.Add(`VERSION 0.8
LET a = b
SET a = c
target:
  ENV env_var = val
  ARG arg_var = val
`)

	f.Fuzz(func(_ *testing.T, input string) {
		_, _ = Parse("Earthfile", input)
	})
}
