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
	tests := []struct {
		earthbuildVer Features
		major      int
		minor      int
		expected   bool
	}{
		{
			earthbuildVer: Features{Major: 0, Minor: 6},
			major:      0,
			minor:      5,
			expected:   true,
		},
		{
			earthbuildVer: Features{Major: 0, Minor: 6},
			major:      0,
			minor:      7,
			expected:   false,
		},
		{
			earthbuildVer: Features{Major: 0, Minor: 6},
			major:      1,
			minor:      2,
			expected:   false,
		},
		{
			earthbuildVer: Features{Major: 1, Minor: 2},
			major:      1,
			minor:      2,
			expected:   true,
		},
	}
	for _, test := range tests {
		test := test
		title := fmt.Sprintf("earthbuild version %d.%d is at least %d.%d",
			test.earthbuildVer.Major, test.earthbuildVer.Minor, test.major, test.minor)
		t.Run(title, func(t *testing.T) {
			t.Parallel()
			actual := versionAtLeast(test.earthbuildVer, test.major, test.minor)
			Equal(t, test.expected, actual)
		})
	}
}
