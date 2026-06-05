package features

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Equal aliases require.Equal. NOTE: if we have significantly more tests
// testing unexported code, these should move to a separate
// imports_unexported_test.go file.
var Equal = require.Equal

func TestVersionAtLeast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		earthVer Features
		major    int
		minor    int
		expected bool
	}{
		{
			earthVer: Features{Major: 0, Minor: 6},
			major:    0,
			minor:    5,
			expected: true,
		},
		{
			earthVer: Features{Major: 0, Minor: 6},
			major:    0,
			minor:    7,
			expected: false,
		},
		{
			earthVer: Features{Major: 0, Minor: 6},
			major:    1,
			minor:    2,
			expected: false,
		},
		{
			earthVer: Features{Major: 1, Minor: 2},
			major:    1,
			minor:    2,
			expected: true,
		},
	}
	for _, test := range tests {
		title := fmt.Sprintf("earthly version %d.%d is at least %d.%d",
			test.earthVer.Major, test.earthVer.Minor, test.major, test.minor)
		t.Run(title, func(t *testing.T) {
			t.Parallel()

			actual := versionAtLeast(test.earthVer, test.major, test.minor)
			Equal(t, test.expected, actual)
		})
	}
}
