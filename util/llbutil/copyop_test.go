package llbutil

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/stretchr/testify/require"
)

// TestCopyLinkOwnershipSelection inspects the LLB graph to distinguish linked
// copies from ordinary copies while checking ownership and working directory.
func TestCopyLinkOwnershipSelection(t *testing.T) {
	t.Parallel()

	const numericOwner = "1234:5678"

	cases := []struct {
		name, owner              string
		link, wantMerge, wantErr bool
	}{
		{"preserved ownership", "", true, true, false},
		{"explicit numeric root", "0:0", true, true, false},
		{"default root alias", "root:root", true, true, false},
		{"numeric nonroot", numericOwner, true, true, false},
		{"named user and group rejected", "app:team", true, false, true},
		{"inherited named user rejected", "app", true, false, true},
		{"mixed numeric user and named group rejected", "1234:team", true, false, true},
		{"mixed named user and numeric group rejected", "app:5678", true, false, true},
		{"root alias and named group rejected", "root:team", true, false, true},
		{"disabled with preserved ownership", "", false, false, false},
		{"disabled with explicit ownership", numericOwner, false, false, false},
		{"disabled with named ownership", "app:team", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			source := pllb.Scratch().File(pllb.Mkfile("/payload", 0o644, []byte("payload"), llb.WithUIDGID(42, 43)))
			destination := pllb.Scratch().File(pllb.Mkfile("/base", 0o644, []byte("base"))).Dir("/work")

			result, err := CopyOp(
				ctx, source, []string{"/payload"}, destination, "/out/payload",
				false, false, false, tc.owner, nil, false, false, tc.link,
			)
			if tc.wantErr {
				require.ErrorContains(t, err, "linked copy cannot resolve user/group names")
				require.ErrorContains(t, err, tc.owner)
				require.ErrorContains(t, err, "use numeric ownership")
				require.ErrorContains(t, err, "VERSION --no-use-copy-link 0.8")

				return
			}

			require.NoError(t, err)

			definition, err := result.Marshal(ctx)
			require.NoError(t, err)

			fileCopy, foundMerge := findPayloadCopy(t, definition)
			require.NotNil(t, fileCopy, "copy operation missing from graph")
			require.Equal(t, tc.wantMerge, foundMerge, "merge operation")

			if tc.owner == "" {
				require.Nil(t, fileCopy.Owner, "preserved ownership unexpectedly received an override")
			} else {
				require.NotNil(t, fileCopy.Owner, "copy lost requested ownership")
			}

			switch tc.owner {
			case numericOwner:
				require.EqualValues(t, 1234, fileCopy.Owner.User.GetByID())
				require.EqualValues(t, 5678, fileCopy.Owner.Group.GetByID())
			case "root:root", "0:0":
				require.EqualValues(t, 0, fileCopy.Owner.User.GetByID())
				require.EqualValues(t, 0, fileCopy.Owner.Group.GetByID())
			}

			cwd, err := result.GetDir(ctx)
			require.NoError(t, err)
			require.Equal(t, "/work", cwd)
		})
	}
}

// findPayloadCopy decodes the copy and merge operations rather than inferring
// linking from payload contents, which can be identical for both copy modes.
func findPayloadCopy(t *testing.T, definition *llb.Definition) (*pb.FileActionCopy, bool) {
	t.Helper()

	var found *pb.FileActionCopy

	foundMerge := false

	for _, data := range definition.Def {
		var operation pb.Op

		err := operation.Unmarshal(data)
		require.NoError(t, err)

		if operation.GetMerge() != nil {
			foundMerge = true
		}

		file := operation.GetFile()
		if file == nil {
			continue
		}

		for _, action := range file.Actions {
			fileCopy := action.GetCopy()
			if fileCopy != nil && fileCopy.Dest == "/out/payload" {
				found = fileCopy
			}
		}
	}

	return found, foundMerge
}
