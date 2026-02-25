package variables

import (
	"testing"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/util/gitutil"
	arg "github.com/EarthBuild/earthbuild/variables/reserved"
)

func TestGetProjectName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tag  string
		safe string
	}{{
		"http://github.com/EarthBuild/earthbuild",
		"EarthBuild/earthbuild",
	}, {
		"http://gitlab.com/EarthBuild/earthbuild",
		"EarthBuild/earthbuild",
	}, {
		"https://github.com/EarthBuild/earthbuild",
		"EarthBuild/earthbuild",
	}, {
		"https://user@github.com/EarthBuild/earthbuild",
		"EarthBuild/earthbuild",
	}, {
		"https://user:password@github.com/EarthBuild/earthbuild",
		"EarthBuild/earthbuild",
	}, {
		"git@github.com:EarthBuild/earthbuild",
		"EarthBuild/earthbuild",
	}, {
		"git@bitbucket.com:EarthBuild/earthbuild",
		"EarthBuild/earthbuild",
	}, {
		"ssh://git@github.com/EarthBuild/earthbuild",
		"EarthBuild/earthbuild",
	}, {
		"ssh://git@github.com:22/EarthBuild/earthbuild",
		"EarthBuild/earthbuild",
	}, {
		"http://github.com/EarthBuild/earthbuild/subdir/anothersubdir",
		"EarthBuild/earthbuild/subdir/anothersubdir",
	}, {
		"http://gitlab.com/EarthBuild/earthbuild/subdir/anothersubdir",
		"EarthBuild/earthbuild/subdir/anothersubdir",
	}, {
		"https://github.com/EarthBuild/earthbuild/subdir/anothersubdir",
		"EarthBuild/earthbuild/subdir/anothersubdir",
	}, {
		"https://user@github.com/EarthBuild/earthbuild/subdir/anothersubdir",
		"EarthBuild/earthbuild/subdir/anothersubdir",
	}, {
		"https://user:password@github.com/EarthBuild/earthbuild/subdir/anothersubdir",
		"EarthBuild/earthbuild/subdir/anothersubdir",
	}, {
		"git@github.com:EarthBuild/earthbuild/subdir/anothersubdir",
		"EarthBuild/earthbuild/subdir/anothersubdir",
	}, {
		"git@bitbucket.com:EarthBuild/earthbuild/subdir/anothersubdir",
		"EarthBuild/earthbuild/subdir/anothersubdir",
	}, {
		"ssh://git@github.com/EarthBuild/earthbuild/subdir/anothersubdir",
		"EarthBuild/earthbuild/subdir/anothersubdir",
	}, {
		"ssh://git@github.com:22/EarthBuild/earthbuild/subdir/anothersubdir",
		"EarthBuild/earthbuild/subdir/anothersubdir",
	}}

	for _, tt := range tests {
		ans := getProjectName(tt.tag)
		Equal(t, tt.safe, ans)
	}
}

func TestBuiltinArgsContentHash(t *testing.T) {
	t.Parallel()

	ftrs := &features.Features{}
	gitMeta := &gitutil.GitMetadata{
		Hash:        "abc123def456abc123def456abc123def456abc1",
		ShortHash:   "abc123de",
		ContentHash: "deadbeef01234567890abcdef01234567890abcd",
	}

	scope := BuiltinArgs(domain.Target{Target: "test"}, nil, gitMeta, DefaultArgs{}, ftrs, false, false)

	val, found := scope.Get(arg.EarthbuildGitContentHash)
	Equal(t, true, found)
	Equal(t, "deadbeef01234567890abcdef01234567890abcd", val)
}

func TestBuiltinArgsContentHashNilGitMeta(t *testing.T) {
	t.Parallel()

	ftrs := &features.Features{}

	scope := BuiltinArgs(domain.Target{Target: "test"}, nil, nil, DefaultArgs{}, ftrs, false, false)

	_, found := scope.Get(arg.EarthbuildGitContentHash)
	Equal(t, false, found)
}
