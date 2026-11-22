package earthfile2llb

import (
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/stretchr/testify/assert"
)

func TestFromError(t *testing.T) {
	t.Parallel()

	ieWithStack := Errorf(earthfile.SourceLocation{
		File:        "path/To/Earthfile",
		StartLine:   90,
		StartColumn: 8,
	}, "", "some stack", "some error message")

	ieWithoutStack := Errorf(earthfile.SourceLocation{
		File:        "path/To/Earthfile",
		StartLine:   90,
		StartColumn: 8,
	}, "", "", "some error message")

	tests := map[string]struct {
		providerErr    error
		expectedResult *InterpreterError
	}{
		"nil error": {},
		"no file path": {
			providerErr: errors.New("line 5:4 some error message"),
		},
		"no line": {
			providerErr: errors.New("path/to/Earthfile 5:4 some error message"),
		},
		"no column": {
			providerErr: errors.New("path/to/Earthfile line 5: missing column"),
		},
		"no error message": {
			providerErr: errors.New("path/to/Earthfile line 5:4"),
		},
		"legacy format without colon": {
			providerErr:    errors.New("path/To/Earthfile:90:8 some error message"),
			expectedResult: ieWithoutStack,
		},
		"legacy format with stack without colon": {
			providerErr:    errors.New("path/To/Earthfile:90:8 some error message\nin\t\tsome stack"),
			expectedResult: ieWithStack,
		},
		"success with stack": {
			providerErr:    ieWithStack,
			expectedResult: ieWithStack,
		},
		"success without stack": {
			providerErr:    ieWithoutStack,
			expectedResult: ieWithoutStack,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ie := FromError(tc.providerErr)
			assert.Equal(t, tc.expectedResult, ie)
		})
	}
}

func TestInterpreterError_Error(t *testing.T) {
	t.Parallel()

	loc := earthfile.SourceLocation{
		File:        "path/To/Earthfile",
		StartLine:   90,
		StartColumn: 8,
	}

	tests := []struct {
		err  *InterpreterError
		name string
		want string
	}{
		{
			name: "location and message",
			err:  Errorf(loc, "", "", "something failed"),
			want: "path/To/Earthfile:90:8: something failed",
		},
		{
			name: "location, message, and cause",
			err:  WrapError(errors.New("root cause"), loc, "", "", "wrapped"),
			want: "path/To/Earthfile:90:8: wrapped: root cause",
		},
		{
			name: "location, message, and stack",
			err:  Errorf(loc, "", "target+build", "something failed"),
			want: "path/To/Earthfile:90:8: something failed\nin\t\ttarget+build",
		},
		{
			name: "zero location message only",
			err:  Errorf(earthfile.SourceLocation{}, "", "", "plain failure"),
			want: "plain failure",
		},
		{
			name: "zero location with cause",
			err:  WrapError(errors.New("underlying"), earthfile.SourceLocation{}, "", "", "context"),
			want: "context: underlying",
		},
		{
			name: "location with empty text",
			err:  Errorf(loc, "", "", ""),
			want: "path/To/Earthfile:90:8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}
