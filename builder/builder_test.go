package builder

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/cleanup"
	"github.com/stretchr/testify/require"
)

// TestTempEarthlyOutDir tests that tempEarthlyOutDir always returns the same directory.
func TestTempEarthlyOutDir(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	b, _ := NewBuilder(context.Background(), Opt{
		CleanCollection: cleanup.NewCollection(),
	})

	outDir1, err := b.tempEarthlyOutDir()
	r.NoError(err)

	outDir2, err := b.tempEarthlyOutDir()
	r.NoError(err)

	b.opt.CleanCollection.Close()

	r.Equal(outDir1, outDir2)
}
