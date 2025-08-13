package inputgraph

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/earthbuild/earthbuild/conslogging"
	"github.com/earthbuild/earthbuild/domain"
	"github.com/stretchr/testify/require"
)

func TestParseProjectCommand(t *testing.T) {
	r := require.New(t)
	target := domain.Target{
		LocalPath: "./testdata/with-docker",
		Target:    "with-docker-load",
	}

	ctx := context.Background()
	cons := conslogging.New(os.Stderr, &sync.Mutex{}, conslogging.NoColor, 0, conslogging.Info, false)

	org, project, err := ParseProjectCommand(ctx, target, cons)
	r.NoError(err)
	r.Equal("earthbuild-technologies", org)
	r.Equal("core", project)
}

func TestParseProjectCommandNoProject(t *testing.T) {
	r := require.New(t)
	target := domain.Target{
		LocalPath: "./testdata/no-project",
		Target:    "no-project",
	}

	ctx := context.Background()
	cons := conslogging.New(os.Stderr, &sync.Mutex{}, conslogging.NoColor, 0, conslogging.Info, false)

	_, _, err := ParseProjectCommand(ctx, target, cons)
	r.Error(err)
}
