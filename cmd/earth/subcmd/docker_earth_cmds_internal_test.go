package subcmd

import (
	"context"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestInvokedAs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		args []string
	}{
		{
			name: "canonical name",
			args: []string{earthCmd, docker2EarthCmdName},
			want: docker2EarthCmdName,
		},
		{
			name: "deprecated alias",
			args: []string{earthCmd, deprecatedDocker2EarthCmdName},
			want: deprecatedDocker2EarthCmdName,
		},
		{
			name: "global flag before alias",
			args: []string{earthCmd, "--verbose", deprecatedDocker2EarthCmdName, "--tag", "x:y"},
			want: deprecatedDocker2EarthCmdName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got string

			root := &cli.Command{
				Name:  earthCmd,
				Flags: []cli.Flag{&cli.BoolFlag{Name: "verbose"}},
				Commands: []*cli.Command{
					{
						Name:    docker2EarthCmdName,
						Aliases: []string{deprecatedDocker2EarthCmdName},
						Flags:   []cli.Flag{&cli.StringFlag{Name: "tag"}},
						Action: func(_ context.Context, cmd *cli.Command) error {
							got = invokedAs(cmd)

							return nil
						},
					},
				},
			}

			err := root.Run(t.Context(), tt.args)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			if got != tt.want {
				t.Errorf("invokedAs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInvokedAsWithoutParent(t *testing.T) {
	t.Parallel()

	cmd := &cli.Command{Name: docker2EarthCmdName}
	if got := invokedAs(cmd); got != docker2EarthCmdName {
		t.Errorf("invokedAs() = %q, want %q", got, docker2EarthCmdName)
	}
}
