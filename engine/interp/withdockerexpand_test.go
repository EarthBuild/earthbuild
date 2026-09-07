package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A variable in a WITH DOCKER flag is expanded, like a variable anywhere else.
//
// `WITH DOCKER --pull alpine:$tag` reached the daemon as the eleven characters
// `alpine:$tag` and the pull failed naming a tag with a dollar in it. The
// corpus has had this since it was written - `tests/with-docker/Earthfile`
// declares `ARG ubuntu_img_tag=26.04` and pulls `ubuntu:$ubuntu_img_tag` - and
// it is one of the failures the Native suite carries.
//
// The cause is that the block's flags are parsed straight off the command's
// arguments: `ParseArgsCleaned` reads `st.Command.Args`, which no expansion has
// touched. So it is not `--pull` that is unexpanded, it is every value any of
// these flags takes, and a test for one of them would leave the rest.
func TestWithDockerFlagsExpandTheirVariables(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, body, want, unwanted string }{
		{
			name:     "pull",
			body:     "    WITH DOCKER --pull alpine:$tag\n        RUN docker images\n    END\n",
			want:     "alpine:3.22",
			unwanted: "$tag",
		},
		{
			name:     "compose",
			body:     "    WITH DOCKER --compose compose-$tag.yml\n        RUN docker images\n    END\n",
			want:     "compose-3.22.yml",
			unwanted: "$tag",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n    ARG tag=3.22\n"+tc.body, testMain)
			if err != nil {
				t.Fatalf("%v", err)
			}

			var all strings.Builder
			for _, n := range p.Graph.Nodes() {
				all.WriteString(strings.Join(n.Op.Args, " "))
				all.WriteString("\n")
				all.WriteString(n.Meta.Description)
				all.WriteString("\n")
			}

			got := all.String()

			if strings.Contains(got, tc.unwanted) {
				t.Errorf("%q reaches the graph unexpanded, so the daemon is"+
					" asked for a name with a dollar in it", tc.unwanted)
			}

			if !strings.Contains(got, tc.want) {
				t.Errorf("the expanded value %q is not in the graph", tc.want)
			}
		})
	}
}
