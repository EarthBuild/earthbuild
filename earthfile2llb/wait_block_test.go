package earthfile2llb

import (
	"testing"

	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/stretchr/testify/require"
)

func TestWaitBlock_IsStateExported(t *testing.T) {
	t.Parallel()

	stateA := pllb.Image("alpine:3.20")
	stateB := pllb.Image("alpine:3.21")
	scratch := pllb.Scratch()

	tests := []struct {
		name  string
		state *pllb.State
		items []states.WaitItem
		want  bool
	}{
		{
			name:  "nil state returns true",
			state: nil,
			want:  true,
		},
		{
			name:  "scratch state with nil output returns true",
			state: &scratch,
			want:  true,
		},
		{
			name:  "empty waitBlock items returns false",
			state: &stateA,
			items: nil,
			want:  false,
		},
		{
			name:  "non-saveImage items ignored",
			state: &stateA,
			items: []states.WaitItem{
				&stateWaitItem{state: &stateA},
			},
			want: false,
		},
		{
			name:  "saveImage item matching state with doPush returns true",
			state: &stateA,
			items: []states.WaitItem{
				&saveImageWaitItem{
					si:     states.SaveImage{State: stateA},
					doPush: true,
				},
			},
			want: true,
		},
		{
			name:  "saveImage item matching state with localExport returns true",
			state: &stateA,
			items: []states.WaitItem{
				&saveImageWaitItem{
					si:          states.SaveImage{State: stateA},
					localExport: true,
				},
			},
			want: true,
		},
		{
			name:  "saveImage matching state without push or localExport returns false",
			state: &stateA,
			items: []states.WaitItem{
				&saveImageWaitItem{
					si:          states.SaveImage{State: stateA},
					doPush:      false,
					localExport: false,
				},
			},
			want: false,
		},
		{
			name:  "saveImage with different state returns false",
			state: &stateA,
			items: []states.WaitItem{
				&saveImageWaitItem{
					si:     states.SaveImage{State: stateB},
					doPush: true,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wb := newWaitBlock()
			for _, item := range tt.items {
				wb.AddItem(item)
			}

			got := wb.isStateExported(tt.state)
			require.Equal(t, tt.want, got)
		})
	}
}
