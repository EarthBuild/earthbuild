package builder

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/earthbuild/earthbuild/cleanup"
)

// TestTempearthbuildOutDir tests that tempearthbuildOutDir always returns the same directory
func TestTempearthbuildOutDir(t *testing.T) {
	b, _ := NewBuilder(context.Background(), Opt{
		CleanCollection: cleanup.NewCollection(),
	})

	outDir1, err := b.tempearthbuildOutDir()
	assert.NoError(t, err)

	outDir2, err := b.tempearthbuildOutDir()
	assert.NoError(t, err)

	b.opt.CleanCollection.Close()

	assert.Equal(t, outDir1, outDir2)
}
