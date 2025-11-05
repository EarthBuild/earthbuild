package logbus

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_gitSSHToURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{
			in:   "git@github.com:earthbuild/earthbuild.git",
			want: "https://github.com/earthbuild/earthbuild",
		},
		{
			in:   "bob@github.com:earthbuild/earthbuild.git",
			want: "https://github.com/earthbuild/earthbuild",
		},
		{
			in:   "bob@random.com:repo.git",
			want: "https://random.com/repo",
		},
		{
			in:   "bob@host.com:main/sub",
			want: "https://host.com/main/sub",
		},
	}

	for _, test := range tests {
		got := gitSSHToURL(test.in)
		if got != test.want {
			assert.Equal(t, test.want, got)
		}
	}
}
