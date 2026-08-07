package earthfile_test

import (
	"bytes"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

func TestTreeFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tree *earthfile.Tree
		want string
	}{
		{
			name: "version and base recipe",
			tree: &earthfile.Tree{
				Version: &earthfile.Version{
					Args: []string{"--use-docker-ignore", "0.8"},
				},
				BaseRecipe: earthfile.Block{
					{
						Command: &earthfile.Command{
							Name: earthfile.CmdArg,
							Args: []string{"GLOBAL_ARG=default"},
						},
					},
				},
			},
			want: `
VERSION --use-docker-ignore 0.8

ARG GLOBAL_ARG=default
`,
		},
		{
			name: "functions",
			tree: &earthfile.Tree{
				Functions: []earthfile.Function{
					{
						Name: "my-function",
						Recipe: earthfile.Block{
							{
								Command: &earthfile.Command{
									Name: earthfile.CmdArg,
									Args: []string{"PARAM"},
								},
							},
						},
					},
				},
			},
			want: `
FUNCTION my-function:
	ARG PARAM
`,
		},
		{
			name: "multiple targets with docs",
			tree: &earthfile.Tree{
				Targets: []earthfile.Target{
					{
						Name: "first",
						Docs: "# First target",
						Recipe: earthfile.Block{
							{
								Command: &earthfile.Command{
									Name: earthfile.CmdFrom,
									Args: []string{"alpine:3.18"},
								},
							},
						},
					},
					{
						Name: "second",
						Recipe: earthfile.Block{
							{
								Command: &earthfile.Command{
									Name: earthfile.CmdBuild,
									Args: []string{"+first"},
								},
							},
						},
					},
				},
			},
			want: `
# First target
first:
	FROM alpine:3.18

second:
	BUILD +first
`,
		},
		{
			name: "FROM DOCKERFILE command single arg",
			tree: &earthfile.Tree{
				Targets: []earthfile.Target{
					{
						Name: "docker-single",
						Recipe: earthfile.Block{
							{
								Command: &earthfile.Command{
									Name: earthfile.CmdFromDockerfile,
									Args: []string{"."},
								},
							},
						},
					},
				},
			},
			want: `
docker-single:
	FROM DOCKERFILE .
`,
		},
		{
			name: "FROM DOCKERFILE command multiple args",
			tree: &earthfile.Tree{
				Targets: []earthfile.Target{
					{
						Name: "docker-multi",
						Recipe: earthfile.Block{
							{
								Command: &earthfile.Command{
									Name: earthfile.CmdFromDockerfile,
									Args: []string{"--target build", "-f Dockerfile", "."},
								},
							},
						},
					},
				},
			},
			want: `
docker-multi:
	FROM DOCKERFILE \
		--target build \
		-f Dockerfile \
		.
`,
		},
		{
			name: "WITH statement",
			tree: &earthfile.Tree{
				Targets: []earthfile.Target{
					{
						Name: "with-test",
						Recipe: earthfile.Block{
							{
								With: &earthfile.WithStatement{
									Command: earthfile.Command{
										Name: earthfile.CmdDocker,
										Args: []string{"--load", "+build"},
									},
									Body: earthfile.Block{
										{
											Command: &earthfile.Command{
												Name:     earthfile.CmdRun,
												ExecMode: true,
												Args:     []string{"docker", "run", "test"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want: `
with-test:
	WITH DOCKER --load +build
		RUN --exec docker run test
	END
`,
		},
		{
			name: "IF statement with ELSE IF and ELSE",
			tree: &earthfile.Tree{
				Targets: []earthfile.Target{
					{
						Name: "if-test",
						Recipe: earthfile.Block{
							{
								If: &earthfile.IfStatement{
									ExecMode:   true,
									Expression: []string{"[", "$VAR", "=", "1", "]"},
									IfBody: earthfile.Block{
										{
											Command: &earthfile.Command{
												Name: earthfile.CmdRun,
												Args: []string{"echo if"},
											},
										},
									},
									ElseIf: []earthfile.ElseIfStatement{
										{
											ExecMode:   true,
											Expression: []string{"[", "$VAR", "=", "2", "]"},
											Body: earthfile.Block{
												{
													Command: &earthfile.Command{
														Name: earthfile.CmdRun,
														Args: []string{"echo elseif"},
													},
												},
											},
										},
									},
									ElseBody: &earthfile.Block{
										{
											Command: &earthfile.Command{
												Name: earthfile.CmdRun,
												Args: []string{"echo else"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want: `
if-test:
	IF --exec [ $VAR = 1 ]
		RUN echo if
	ELSE IF --exec [ $VAR = 2 ]
		RUN echo elseif
	ELSE
		RUN echo else
	END
`,
		},
		{
			name: "TRY statement with CATCH and FINALLY",
			tree: &earthfile.Tree{
				Targets: []earthfile.Target{
					{
						Name: "try-test",
						Recipe: earthfile.Block{
							{
								Try: &earthfile.TryStatement{
									TryBody: earthfile.Block{
										{
											Command: &earthfile.Command{
												Name: earthfile.CmdRun,
												Args: []string{"echo try"},
											},
										},
									},
									CatchBody: &earthfile.Block{
										{
											Command: &earthfile.Command{
												Name: earthfile.CmdRun,
												Args: []string{"echo catch"},
											},
										},
									},
									FinallyBody: &earthfile.Block{
										{
											Command: &earthfile.Command{
												Name: earthfile.CmdRun,
												Args: []string{"echo finally"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want: `
try-test:
	TRY
		RUN echo try
	CATCH
		RUN echo catch
	FINALLY
		RUN echo finally
	END
`,
		},
		{
			name: "FOR statement",
			tree: &earthfile.Tree{
				Targets: []earthfile.Target{
					{
						Name: "for-test",
						Recipe: earthfile.Block{
							{
								For: &earthfile.ForStatement{
									Args: []string{"i", "IN", "1", "2", "3"},
									Body: earthfile.Block{
										{
											Command: &earthfile.Command{
												Name: earthfile.CmdRun,
												Args: []string{"echo $i"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want: `
for-test:
	FOR i IN 1 2 3
		RUN echo $i
	END
`,
		},
		{
			name: "WAIT statement",
			tree: &earthfile.Tree{
				Targets: []earthfile.Target{
					{
						Name: "wait-test",
						Recipe: earthfile.Block{
							{
								Wait: &earthfile.WaitStatement{
									Body: earthfile.Block{
										{
											Command: &earthfile.Command{
												Name: earthfile.CmdBuild,
												Args: []string{"+other"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			want: `
wait-test:
	WAIT
		BUILD +other
	END
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			err := tt.tree.Format(&buf)
			if err != nil {
				t.Fatalf("Format() unexpected error = %v", err)
			}

			if got := buf.String(); got != tt.want {
				t.Errorf("Format() mismatch.\nGOT:\n%s\nWANT:\n%s", got, tt.want)
			}
		})
	}
}
