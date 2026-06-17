package parse

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

//nolint:goconst
func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  Tree
	}{
		{
			name: "basic target",
			input: `VERSION 0.8
build:
  FROM alpine:3.18
  RUN echo hello
`,
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			want: Tree{
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
			name: "ARG with empty value",
			input: `VERSION 0.8
build:
  ARG myarg=
`,
			want: Tree{
				Version: &Version{
					Args: []string{"0.8"},
				},
				Targets: []Target{
					{
						Name: "build",
						Recipe: Block{
							{
								Command: &Command{
									Name: "ARG",
									Args: []string{"myarg", "=", ""},
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

func zeroSourceLocations(ef *Tree) {
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
