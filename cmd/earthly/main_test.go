package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsForceColor(t *testing.T) {
	testCases := []struct {
		name       string
		forceColor string
		want       bool
	}{
		{
			name:       "FORCE_COLOR not set",
			forceColor: "",
			want:       false,
		},
		{
			name:       "FORCE_COLOR set to true",
			forceColor: "1",
			want:       true,
		},
		{
			name:       "FORCE_COLOR set to false",
			forceColor: "0",
			want:       false,
		},
		{
			name:       "FORCE_COLOR invalid",
			forceColor: "invalid",
			want:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Do not use t.Parallel() because t.Setenv modifies process-wide state.
			if tc.forceColor != "" {
				t.Setenv("FORCE_COLOR", tc.forceColor)
			} else {
				t.Setenv("FORCE_COLOR", "")
			}

			mode := isForceColor()

			assert.Equal(t, tc.want, mode)
		})
	}
}
