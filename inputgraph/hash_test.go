package inputgraph

import (
	"context"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/stretchr/testify/require"
)

func TestHashTargetWithDocker(t *testing.T) {
	t.Parallel()

	r := require.New(t)
	target := domain.Target{
		LocalPath: "./testdata/with-docker",
		Target:    "with-docker-load",
	}

	ctx := context.Background()
	cons := conslogging.New(os.Stderr, &sync.Mutex{}, conslogging.NoColor, 0, conslogging.Info, false)

	hashOpt := HashOpt{Console: cons, Target: target}
	hash, _, err := HashTarget(ctx, hashOpt)
	r.NoError(err)

	first := hex.EncodeToString(hash)
	r.NotEmpty(first)

	path := "./testdata/with-docker/Earthfile"
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "Earthfile")

	err = copyFile(path, tmpFile)
	r.NoError(err)

	err = replaceInFile(tmpFile, "saved:latest", "other:latest")
	r.NoError(err)

	target = domain.Target{
		LocalPath: tmpDir,
		Target:    "with-docker-load",
	}

	hashOpt = HashOpt{Console: cons, Target: target}
	hash, _, err = HashTarget(ctx, hashOpt)
	r.NoError(err)

	second := hex.EncodeToString(hash)
	r.NotEmpty(second)
	r.NotEqual(first, second)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst) // #nosec G304
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	return nil
}

func replaceInFile(path, find, replace string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0) // #nosec G304
	if err != nil {
		return err
	}
	defer f.Close()

	dataBytes, err := io.ReadAll(f)
	if err != nil {
		return err
	}

	data := string(dataBytes)
	data = strings.ReplaceAll(data, find, replace)

	_, err = f.Seek(0, 0)
	if err != nil {
		return err
	}

	_, err = f.WriteString(data)
	if err != nil {
		return err
	}

	return nil
}

func TestHashTargetWithDockerNoAlias(t *testing.T) {
	t.Parallel()

	r := require.New(t)
	target := domain.Target{
		LocalPath: "./testdata/with-docker",
		Target:    "with-docker-load-no-alias",
	}

	ctx := context.Background()
	cons := conslogging.New(os.Stderr, &sync.Mutex{}, conslogging.NoColor, 0, conslogging.Info, false)

	hashOpt := HashOpt{Console: cons, Target: target}
	hash, _, err := HashTarget(ctx, hashOpt)
	r.NoError(err)

	hex := hex.EncodeToString(hash)
	r.NotEmpty(hex)
}

func TestHashTargetWithDockerRemote(t *testing.T) {
	t.Parallel()

	r := require.New(t)
	target := domain.Target{
		LocalPath: "./testdata/with-docker",
		Target:    "with-docker-load-remote",
	}

	ctx := context.Background()
	cons := conslogging.New(os.Stderr, &sync.Mutex{}, conslogging.NoColor, 0, conslogging.Info, false)

	hashOpt := HashOpt{Console: cons, Target: target}
	hash, _, err := HashTarget(ctx, hashOpt)
	r.NoError(err)

	r.NotEmpty(hex.EncodeToString(hash))
}

func TestHashTargetNoCache(t *testing.T) {
	t.Parallel()

	r := require.New(t)
	target := domain.Target{
		LocalPath: "./testdata/target-cache",
		Target:    "no-cache-hits",
	}

	ctx := context.Background()
	cons := conslogging.New(os.Stderr, &sync.Mutex{}, conslogging.NoColor, 0, conslogging.Info, false)

	hashOpt := HashOpt{Console: cons, Target: target}
	hash, stats, err := HashTarget(ctx, hashOpt)
	r.NoError(err)

	r.Equal(3, stats.TargetsHashed)
	r.Equal(0, stats.TargetCacheHits)

	enc := hex.EncodeToString(hash)
	r.NotEmpty(enc)
}

func TestHashTargetCache(t *testing.T) {
	t.Parallel()

	r := require.New(t)
	target := domain.Target{
		LocalPath: "./testdata/target-cache",
		Target:    "cache-hits",
	}

	ctx := context.Background()
	cons := conslogging.New(os.Stderr, &sync.Mutex{}, conslogging.NoColor, 0, conslogging.Info, false)

	hashOpt := HashOpt{Console: cons, Target: target}
	hash, stats, err := HashTarget(ctx, hashOpt)
	r.NoError(err)

	r.Equal(3, stats.TargetsHashed)
	r.Equal(4, stats.TargetCacheHits)

	hex := hex.EncodeToString(hash)
	r.NotEmpty(hex)
}
