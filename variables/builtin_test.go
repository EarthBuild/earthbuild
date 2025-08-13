package variables

import (
	"testing"
)

func TestGetProjectName(t *testing.T) {
	var tests = []struct {
		tag  string
		safe string
	}{
		{"http://github.com/earthbuild/earthbuild", "earthbuild/earthbuild"},
		{"http://gitlab.com/earthbuild/earthbuild", "earthbuild/earthbuild"},
		{"https://github.com/earthbuild/earthbuild", "earthbuild/earthbuild"},
		{"https://user@github.com/earthbuild/earthbuild", "earthbuild/earthbuild"},
		{"https://user:password@github.com/earthbuild/earthbuild", "earthbuild/earthbuild"},
		{"git@github.com:earthbuild/earthbuild", "earthbuild/earthbuild"},
		{"git@bitbucket.com:earthbuild/earthbuild", "earthbuild/earthbuild"},
		{"ssh://git@github.com/earthbuild/earthbuild", "earthbuild/earthbuild"},
		{"ssh://git@github.com:22/earthbuild/earthbuild", "earthbuild/earthbuild"},
		{"http://github.com/earthbuild/earthbuild/subdir/anothersubdir", "earthbuild/earthbuild/subdir/anothersubdir"},
		{"http://gitlab.com/earthbuild/earthbuild/subdir/anothersubdir", "earthbuild/earthbuild/subdir/anothersubdir"},
		{"https://github.com/earthbuild/earthbuild/subdir/anothersubdir", "earthbuild/earthbuild/subdir/anothersubdir"},
		{"https://user@github.com/earthbuild/earthbuild/subdir/anothersubdir", "earthbuild/earthbuild/subdir/anothersubdir"},
		{"https://user:password@github.com/earthbuild/earthbuild/subdir/anothersubdir", "earthbuild/earthbuild/subdir/anothersubdir"},
		{"git@github.com:earthbuild/earthbuild/subdir/anothersubdir", "earthbuild/earthbuild/subdir/anothersubdir"},
		{"git@bitbucket.com:earthbuild/earthbuild/subdir/anothersubdir", "earthbuild/earthbuild/subdir/anothersubdir"},
		{"ssh://git@github.com/earthbuild/earthbuild/subdir/anothersubdir", "earthbuild/earthbuild/subdir/anothersubdir"},
		{"ssh://git@github.com:22/earthbuild/earthbuild/subdir/anothersubdir", "earthbuild/earthbuild/subdir/anothersubdir"},
	}

	for _, tt := range tests {
		ans := getProjectName(tt.tag)
		Equal(t, tt.safe, ans)
	}
}
