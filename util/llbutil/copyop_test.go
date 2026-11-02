package llbutil

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/stretchr/testify/require"
)

func TestCopyLinkOwnershipSelection(t *testing.T) {
	t.Parallel()

	const numericOwner = "1234:5678"

	cases := []struct {
		name, owner     string
		link, wantMerge bool
	}{
		{"preserved ownership", "", true, true},
		{"explicit numeric root", "0:0", true, true},
		{"default root alias", "root:root", true, true},
		{"numeric nonroot", numericOwner, true, true},
		{"named owner needs base", "app:team", true, false},
		{"named group needs base", "1234:team", true, false},
		{"disabled with preserved ownership", "", false, false},
		{"disabled with explicit ownership", numericOwner, false, false},
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
