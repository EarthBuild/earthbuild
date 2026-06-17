package parse

import (
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/google/go-cmp/cmp"
)

//nolint:goconst
func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  earthfile.Earthfile
	}{
		{
			name: "basic target",
			input: `VERSION 0.8
build:
  FROM alpine:3.18
  RUN echo hello
`,
			want: earthfile.Earthfile{
				Version: &earthfile.Version{
					Args: []string{"0.8"},
				},
				Targets: []earthfile.Target{
					{
						Name: "build",
						Recipe: earthfile.Block{
							{
								Command: &earthfile.Command{
									Name: "FROM",
									Args: []string{"alpine:3.18"},
								},
							},
							{
								Command: &earthfile.Command{
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
			want: earthfile.Earthfile{
				Version: &earthfile.Version{
					Args: []string{"0.8"},
				},
				Targets: []earthfile.Target{
					{
						Name: "build",
						Recipe: earthfile.Block{
							{
								If: &earthfile.IfStatement{
									Expression: []string{"[", "\"$VAR\"", "=", "\"1\"", "]"},
									IfBody: earthfile.Block{
										{
											Command: &earthfile.Command{
												Name: "RUN",
												Args: []string{"echo", "\"yes\""},
											},
										},
									},
									ElseIf: []earthfile.ElseIf{
										{
											Expression: []string{"[", "\"$VAR\"", "=", "\"2\"", "]"},
											Body: earthfile.Block{
												{
													Command: &earthfile.Command{
														Name: "RUN",
														Args: []string{"echo", "\"no\""},
													},
												},
											},
										},
									},
									ElseBody: &earthfile.Block{
										{
											Command: &earthfile.Command{
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
			want: earthfile.Earthfile{
				Version: &earthfile.Version{
					Args: []string{"0.8"},
				},
				Targets: []earthfile.Target{
					{
						Name: "build",
						Recipe: earthfile.Block{
							{
								For: &earthfile.ForStatement{
									Args: []string{"arg", "IN", "foo", "bar"},
									Body: earthfile.Block{
										{
											Command: &earthfile.Command{
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
			want: earthfile.Earthfile{
				Version: &earthfile.Version{
					Args: []string{"0.8"},
				},
				Targets: []earthfile.Target{
					{
						Name: "build",
						Recipe: earthfile.Block{
							{
								Try: &earthfile.TryStatement{
									TryBody: earthfile.Block{
										{
											Command: &earthfile.Command{
												Name: "RUN",
												Args: []string{"echo", "\"try\""},
											},
										},
									},
									CatchBody: &earthfile.Block{
										{
											Command: &earthfile.Command{
												Name: "RUN",
												Args: []string{"echo", "\"catch\""},
											},
										},
									},
									FinallyBody: &earthfile.Block{
										{
											Command: &earthfile.Command{
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
			want: earthfile.Earthfile{
				Version: &earthfile.Version{
					Args: []string{"0.8"},
				},
				Targets: []earthfile.Target{
					{
						Name: "build",
						Recipe: earthfile.Block{
							{
								With: &earthfile.WithStatement{
									Command: earthfile.Command{
										Name: "DOCKER",
										Args: []string{"--pull", "alpine:3.18"},
									},
									Body: earthfile.Block{
										{
											Command: &earthfile.Command{
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
			want: earthfile.Earthfile{
				Version: &earthfile.Version{
					Args: []string{"0.8"},
				},
				Targets: []earthfile.Target{
					{
						Name: "build",
						Recipe: earthfile.Block{
							{
								Wait: &earthfile.WaitStatement{
									Body: earthfile.Block{
										{
											Command: &earthfile.Command{
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
			want: earthfile.Earthfile{
				Version: &earthfile.Version{
					Args: []string{"0.8"},
				},
				Functions: []earthfile.Function{
					{
						Name: "COMMAND my-command",
						Recipe: earthfile.Block{
							{
								Command: &earthfile.Command{
									Name: "FROM",
									Args: []string{"alpine:3.18"},
								},
							},
							{
								Command: &earthfile.Command{
									Name: "RUN",
									Args: []string{"echo", "\"cmd\""},
								},
							},
						},
					},
					{
						Name: "FUNCTION my-func",
						Recipe: earthfile.Block{
							{
								Command: &earthfile.Command{
									Name: "FROM",
									Args: []string{"alpine:3.18"},
								},
							},
							{
								Command: &earthfile.Command{
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
			want: earthfile.Earthfile{
				Version: &earthfile.Version{
					Args: []string{"0.8"},
				},
				Targets: []earthfile.Target{
					{
						Name: "build",
						Docs: "this is a comment\n",
						Recipe: earthfile.Block{
							{
								Command: &earthfile.Command{
									Name: "RUN",
									Args: []string{"echo", "\"hello \\\n    world $(echo 'nested') and $VAR\""},
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

func zeroSourceLocations(ef *earthfile.Earthfile) {
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

func zeroBlockSourceLocations(block earthfile.Block) {
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
