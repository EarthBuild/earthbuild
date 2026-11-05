package earthfile2llb

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	debuggercommon "github.com/EarthBuild/earthbuild/debugger/common"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/util/gatewaycrafter"
)

// TestTryFinallySaveFiles is a regression test for #578: SAVE ARTIFACT ... AS
// LOCAL inside FINALLY must resolve relative to the Earthfile's directory, not
// the directory earth was run from, when the TRY body fails.
func TestTryFinallySaveFiles(t *testing.T) {
	t.Parallel()

	const src = "foo"

	root := t.TempDir()

	err := os.Mkdir(filepath.Join(root, "sub"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	// Target local paths are relative to the working directory; express the
	// temp dir that way so the test exercises the same resolution as a build.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	rootRel, err := filepath.Rel(cwd, root)
	if err != nil {
		t.Fatal(err)
	}

	subRel := filepath.Join(rootRel, "sub")

	tests := []struct {
		name          string
		localPath     string
		in            []debuggercommon.SaveFilesSettings
		want          []debuggercommon.SaveFilesSettings
		wantWhiteList []string
		wantErr       bool
	}{
		{
			name:      "earthfile in working directory",
			localPath: rootRel,
			in:        []debuggercommon.SaveFilesSettings{{Src: src, Dst: "foo"}},
			want: []debuggercommon.SaveFilesSettings{
				{Src: src, Dst: filepath.Join(root, "foo")},
			},
			wantWhiteList: []string{"foo", filepath.Join(root, "foo")},
		},
		{
			name:      "earthfile in sub directory",
			localPath: subRel,
			in: []debuggercommon.SaveFilesSettings{
				{Src: src, Dst: "./foo", IfExists: true},
				{Src: "data", Dst: "out/data"},
			},
			want: []debuggercommon.SaveFilesSettings{
				{Src: src, Dst: filepath.Join(root, "sub", "foo"), IfExists: true},
				{Src: "data", Dst: filepath.Join(root, "sub", "out", "data")},
			},
			wantWhiteList: []string{
				"./foo",
				"out/data",
				filepath.Join(root, "sub", "foo"),
				filepath.Join(root, "sub", "out", "data"),
			},
		},
		{
			name:      "absolute destination under earthfile directory",
			localPath: subRel,
			in: []debuggercommon.SaveFilesSettings{
				{Src: src, Dst: filepath.Join(root, "sub", "foo")},
			},
			want: []debuggercommon.SaveFilesSettings{
				{Src: src, Dst: filepath.Join(root, "sub", "foo")},
			},
			wantWhiteList: []string{filepath.Join(root, "sub", "foo")},
		},
		{
			name:      "destination outside earthfile directory",
			localPath: subRel,
			in:        []debuggercommon.SaveFilesSettings{{Src: src, Dst: "../foo"}},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			whiteList := gatewaycrafter.NewLocalArtifactWhiteList()
			c := &Converter{
				target: domain.Target{LocalPath: tt.localPath},
				opt:    ConvertOpt{LocalArtifactWhiteList: whiteList},
			}

			got, err := c.tryFinallySaveFiles(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("tryFinallySaveFiles() = %v, want error", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("tryFinallySaveFiles() error = %v", err)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("tryFinallySaveFiles() = %v, want %v", got, tt.want)
			}

			gotWhiteList := whiteList.AsList()
			slices.Sort(gotWhiteList)
			slices.Sort(tt.wantWhiteList)

			if !slices.Equal(gotWhiteList, tt.wantWhiteList) {
				t.Errorf("white list = %v, want %v", gotWhiteList, tt.wantWhiteList)
			}
		})
	}
}
