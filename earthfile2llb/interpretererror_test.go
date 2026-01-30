package earthfile2llb

import (
	"testing"

	"github.com/EarthBuild/earthbuild/ast/spec"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestFromError(t *testing.T) {
	t.Parallel()

	ieWithStack := Errorf(&spec.SourceLocation{
		File:        "path/To/Earthfile",
		StartLine:   90,
		StartColumn: 8,
	}, "", "some stack", "some error message")

	ieWithoutStack := Errorf(&spec.SourceLocation{
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
			providerErr: errors.New("path/to/Earthfile line 5:"),
		},
		"no error message": {
			providerErr: errors.New("path/to/Earthfile line 5:4"),
		},
		"success without stack": {
			providerErr:    ieWithStack,
			expectedResult: ieWithStack,
		},
		"success with stack": {
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
