package earthfile2llb

import (
	"sync"
	"testing"
	"testing/synctest"

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

func TestConverter_IsStateExported(t *testing.T) {
	t.Parallel()

	stateA := pllb.Image("alpine:3.20")
	stateB := pllb.Image("alpine:3.21")

	tests := []struct {
		state *pllb.State
		c     *Converter
		name  string
		want  bool
	}{
		{
			name:  "nil state returns true",
			state: nil,
			c:     &Converter{},
			want:  true,
		},
		{
			name: "matching state in waitBlockStack returns true",
			c: func() *Converter {
				wb := newWaitBlock()
				wb.AddItem(&saveImageWaitItem{
					si:     states.SaveImage{State: stateA},
					doPush: true,
				})

				return &Converter{
					waitBlockStack: []*waitBlock{wb},
				}
			}(),
			state: &stateA,
			want:  true,
		},
		{
			name: "matching state in mts.Final.SaveImages with push returns true",
			c: &Converter{
				opt: ConvertOpt{DoPushes: true},
				mts: &states.MultiTarget{
					Final: &states.SingleTarget{
						SaveImages: []states.SaveImage{
							{
								DockerTag: "registry.example.com/test:latest",
								State:     stateA,
								Push:      true,
							},
						},
					},
				},
			},
			state: &stateA,
			want:  true,
		},
		{
			name: "different state in SaveImages returns false",
			c: &Converter{
				opt: ConvertOpt{DoPushes: true},
				mts: &states.MultiTarget{
					Final: &states.SingleTarget{
						SaveImages: []states.SaveImage{
							{
								DockerTag: "registry.example.com/test:latest",
								State:     stateB,
								Push:      true,
							},
						},
					},
				},
			},
			state: &stateA,
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.c.isStateExported(tt.state)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestWaitBlock_IsStateExported_Concurrent(t *testing.T) {
	t.Parallel()

	wb := newWaitBlock()
	state := pllb.Image("alpine:3.20")

	const iterations = 100

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()

		for range iterations {
			itemState := pllb.Image("alpine:3.20")

			wb.AddItem(&saveImageWaitItem{
				doPush: true,
				si: states.SaveImage{
					DockerTag: "test:latest",
					State:     itemState,
				},
			})
		}
	}()

	go func() {
		defer wg.Done()

		for range iterations {
			_ = wb.isStateExported(&state)
		}
	}()

	wg.Wait()
}

func TestWaitBlock_Wait_WithExportedState(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		wb := newWaitBlock()
		state := pllb.Image("alpine:3.20")

		wb.AddItem(&saveImageWaitItem{
			doPush: true,
			si: states.SaveImage{
				DockerTag: "test:latest",
				State:     state,
			},
		})

		wb.AddItem(&stateWaitItem{
			state: &state,
		})

		err := wb.Wait(t.Context(), true, false)
		require.NoError(t, err)
	})
}
