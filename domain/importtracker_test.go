package domain

import (
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImports(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	tests := []struct {
		importStr string
		as        string
		ref       string
		expected  string
		ok        bool
	}{
		{"github.com/foo/a", "", "a+abc", "github.com/foo/a+abc", true},
		{"github.com/foo/b", "b", "b+abc", "github.com/foo/b+abc", true},
		{"github.com/foo/c", "1", "c+abc", "", false},
		{"github.com/foo/d:v1.2.3", "", "d+abc", "github.com/foo/d:v1.2.3+abc", true},
		{"github.com/foo/e:v1.2.3", "e", "e+abc", "github.com/foo/e:v1.2.3+abc", true},
		{"github.com/foo/f:v1.2.3", "2", "f+abc", "", false},
		{"./foo/g", "", "g+abc", "./foo/g+abc", true},
		{"./foo/i", "3", "3+abc", "./foo/i+abc", true},
		{"./foo/j", "4", "j+abc", "", false},
		{"../foo/k", "", "k+abc", "../foo/k+abc", true},
		{"../foo/l", "5", "5+abc", "../foo/l+abc", true},
		{"../foo/m", "6", "m+abc", "", false},
		{"/foo/n", "", "n+abc", "/foo/n+abc", true},
		{"/foo/o", "7", "7+abc", "/foo/o+abc", true},
		{"/foo/p", "8", "p+abc", "", false},
	}

	var console conslogging.ConsoleLogger

	for _, tt := range tests {
		ir := NewImportTracker(console, nil)
		err := ir.Add(tt.importStr, tt.as, false, false, false)
		r.NoError(err, "add import error")

		ref, err := ParseTarget(tt.ref)
		r.NoError(err, "parse test case ref") // check that the test data is good
		assert.Equal(t, tt.ref, ref.String()) // sanity check

		ref2, _, _, err := ir.Deref(ref)
		if tt.ok {
			r.NoError(err, "deref import")
			assert.Equal(t, tt.expected, ref2.StringCanonical()) // StringCanonical shows its resolved form
			assert.Equal(t, tt.ref, ref2.String())               // String shows its import form
		} else {
			assert.Error(t, err, "deref should have error'd")
		}
	}
}
