package builder

import (
	"testing"

	"github.com/EarthBuild/earthbuild/cleanup"
	"github.com/stretchr/testify/require"
)

// TestTempEarthOutDir tests that tempEarthOutDir always returns the same directory.
func TestTempEarthOutDir(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	b, _ := NewBuilder(Opt{
		CleanCollection: cleanup.NewCollection(),
	})

	outDir1, err := b.tempEarthOutDir()
	r.NoError(err)

	outDir2, err := b.tempEarthOutDir()
	r.NoError(err)

	b.opt.CleanCollection.Close()

	r.Equal(outDir1, outDir2)
}
