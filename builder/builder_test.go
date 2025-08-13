package builder

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/earthbuild/earthbuild/cleanup"
)

// TestTempEarthbuildOutDir tests that tempEarthbuildOutDir always returns the same directory
func TestTempEarthbuildOutDir(t *testing.T) {
	b, _ := NewBuilder(context.Background(), Opt{
		CleanCollection: cleanup.NewCollection(),
	})

	outDir1, err := b.tempEarthbuildOutDir()
	assert.NoError(t, err)

	outDir2, err := b.tempEarthbuildOutDir()
	assert.NoError(t, err)

	b.opt.CleanCollection.Close()

	assert.Equal(t, outDir1, outDir2)
}
