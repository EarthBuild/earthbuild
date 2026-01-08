package variables

import (
	"testing"
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
