package image

import "testing"

// A challenge is parsed into the endpoint that issues its token, and the scope
// survives intact.
func TestAChallengeKeepsTheScopeItAsksFor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, challenge, want string
	}{
		{
			name:      "a read",
			challenge: `Bearer realm="https://auth.example/token",service="registry",scope="repository:library/alpine:pull"`,
			want:      "https://auth.example/token?service=registry&scope=repository:library/alpine:pull",
		},
		{
			// The comma inside the value is the whole point: a write scope
			// always has one, and splitting on it yields a token that reads.
			name:      "a write",
			challenge: `Bearer realm="https://auth.example/token",service="registry",scope="repository:app:pull,push"`,
			want:      "https://auth.example/token?service=registry&scope=repository:app:pull,push",
		},
		{
			name:      "no scope offered",
			challenge: `Bearer realm="https://auth.example/token",service="registry"`,
			want:      "https://auth.example/token?service=registry&scope=",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tokenEndpoint(tc.challenge)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// A challenge this engine cannot answer says so, rather than proceeding with an
// endpoint it made up.
func TestAnUnusableChallengeIsRefused(t *testing.T) {
	t.Parallel()

	for _, challenge := range []string{
		`Basic realm="example"`,
		`Bearer service="registry",scope="repository:app:pull,push"`,
	} {
		_, err := tokenEndpoint(challenge)
		if err == nil {
			t.Errorf("%q was accepted", challenge)
		}
	}
}
