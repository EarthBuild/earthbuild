package stringutil

import (
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/logstream"
	"github.com/stretchr/testify/assert"
)

func Test_EnumToString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input    ProtoEnum
		f        EnumToStringFunc
		expected string
	}{
		"Title": {
			input:    logstream.FailureType_FAILURE_TYPE_BUILDKIT_CRASHED,
			f:        Title,
			expected: "Buildkit Crashed",
		},
		"Lower": {
			input:    logstream.FailureType_FAILURE_TYPE_BUILDKIT_CRASHED,
			f:        Lower,
			expected: "buildkit crashed",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res := tc.f(tc.input)
			assert.Equal(t, tc.expected, res)
		})
	}
}

// Title and Lower must be safe for concurrent use; x/text Casers are
// stateful, so sharing one across goroutines corrupts its internal state
// (slice bounds panics under -race in CI).
func Test_EnumToString_Concurrent(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 200 {
				assert.Equal(t, "Buildkit Crashed", Title(logstream.FailureType_FAILURE_TYPE_BUILDKIT_CRASHED))
				assert.Equal(t, "connection failure", Lower(logstream.FailureType_FAILURE_TYPE_CONNECTION_FAILURE))
			}
		})
	}

	wg.Wait()
}

func Test_EnumToStringArray(t *testing.T) {
	t.Parallel()

	input := []ProtoEnum{
		logstream.FailureType_FAILURE_TYPE_BUILDKIT_CRASHED,
		logstream.FailureType_FAILURE_TYPE_CONNECTION_FAILURE,
	}
	res := EnumToStringArray(input, Title)
	assert.Equal(t, []string{"Buildkit Crashed", "Connection Failure"}, res)
}
