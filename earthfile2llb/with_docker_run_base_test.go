package earthfile2llb

import "testing"

// Test_stripImageDigest covers the retag target produced for WITH DOCKER pulls.
// Docker engine refuses `docker tag <src> <dst>` when <dst> carries an
// `@sha256:...` (or any digest-algorithm) suffix, so we strip it before retagging.
// Regression: https://github.com/EarthBuild/earthbuild/issues/512
func Test_stripImageDigest(t *testing.T) {
	t.Parallel()

	const digest64 = "d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc"

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no digest, tag-only",
			in:   "alpine:3.20",
			want: "alpine:3.20",
		},
		{
			name: "no digest, no tag",
			in:   "alpine",
			want: "alpine",
		},
		{
			name: "tag plus digest",
			in:   "alpine:3.20@sha256:" + digest64,
			want: "alpine:3.20",
		},
		{
			name: "digest only, no tag",
			in:   "alpine@sha256:" + digest64,
			want: "alpine",
		},
		{
			name: "registry path with port, tag, digest",
			in:   "registry.example.com:5000/team/app:v1.2.3@sha256:" + digest64,
			want: "registry.example.com:5000/team/app:v1.2.3",
		},
		{
			name: "non-sha256 digest algorithm",
			in:   "alpine:3.20@sha512:" + digest64 + digest64,
			want: "alpine:3.20",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := stripImageDigest(tc.in)
			if got != tc.want {
				t.Errorf("stripImageDigest(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
