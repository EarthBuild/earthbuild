package earthfile2llb

import (
	"testing"

	"github.com/EarthBuild/earthbuild/features"
	"github.com/stretchr/testify/require"
)

// childSaveReferenced narrows only the referencing axis. The companion
// requirement -- that Export is not narrowed with it -- is what keeps a wait
// item's later SetDoSave honest; see the comment on childSaveReferenced.
func TestChildSaveReferenced(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		referencedSaveOnly    bool
		parentSaveReferenced  bool
		onlyFinalTargetImages bool
		cmdT                  cmdType
		targetIsRemote        bool
		want                  bool
	}{
		{
			name:                 "BUILD of a referenced parent stays referenced",
			referencedSaveOnly:   true,
			parentSaveReferenced: true,
			cmdT:                 buildCmd,
			want:                 true,
		},
		{
			name:                 "FROM is not a reference",
			referencedSaveOnly:   true,
			parentSaveReferenced: true,
			cmdT:                 fromCmd,
			want:                 false,
		},
		{
			name:                 "COPY is not a reference",
			referencedSaveOnly:   true,
			parentSaveReferenced: true,
			cmdT:                 copyCmd,
			want:                 false,
		},
		{
			name:                 "an unreferenced parent does not make its child referenced",
			referencedSaveOnly:   true,
			parentSaveReferenced: false,
			cmdT:                 buildCmd,
			want:                 false,
		},
		{
			name:                  "image mode drops non-final saves",
			referencedSaveOnly:    true,
			parentSaveReferenced:  true,
			onlyFinalTargetImages: true,
			cmdT:                  buildCmd,
			want:                  false,
		},
		{
			name:                 "legacy: local target stays referenced through FROM",
			referencedSaveOnly:   false,
			parentSaveReferenced: true,
			cmdT:                 fromCmd,
			targetIsRemote:       false,
			want:                 true,
		},
		{
			name:                 "legacy: remote target is not referenced",
			referencedSaveOnly:   false,
			parentSaveReferenced: true,
			cmdT:                 buildCmd,
			targetIsRemote:       true,
			want:                 false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &Converter{opt: ConvertOpt{
				SaveReferenced:        tt.parentSaveReferenced,
				OnlyFinalTargetImages: tt.onlyFinalTargetImages,
				Features:              &features.Features{ReferencedSaveOnly: tt.referencedSaveOnly},
			}}

			require.Equal(t, tt.want, c.childSaveReferenced(tt.cmdT, tt.targetIsRemote))
		})
	}
}

// Every Export must survive narrowing unchanged. A wait item consults Export
// when SetDoSave arrives down a BUILD edge, which can be long after this point,
// so narrowing it here would make that read a stale value and drop the save.
func TestChildSaveReferencedDoesNotNarrowExport(t *testing.T) {
	t.Parallel()

	for _, export := range []Export{ExportAll, ExportArtifactsOnly, ExportNone} {
		t.Run(export.String(), func(t *testing.T) {
			t.Parallel()

			opt := ConvertOpt{
				Export:         export,
				SaveReferenced: true,
				Features:       &features.Features{ReferencedSaveOnly: true},
			}
			c := &Converter{opt: opt}

			// fromCmd is the case that turns referencing off, and is exactly where
			// Export used to be clobbered.
			require.False(t, c.childSaveReferenced(fromCmd, false))
			require.Equal(t, export, c.opt.Export, "Export must not be narrowed per target")
		})
	}
}
