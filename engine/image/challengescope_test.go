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

			got, err := tokenEndpoint(tc.challenge, "https://registry.example/v2/")
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
		_, err := tokenEndpoint(challenge, "https://registry.example/v2/")
		if err == nil {
			t.Errorf("%q was accepted", challenge)
		}
	}
}

// A token realm cannot take credentials somewhere a registry spoken to over TLS
// would not.
//
// The realm is the registry's to name and need not be its own host - Docker Hub
// sends `auth.docker.io` - but credentials go to it as basic auth, so an
// `http://` realm from an HTTPS registry is the user's password in clear text.
// A registry already spoken to in plain HTTP (loopback, or configured so) has
// nothing left to downgrade.
func TestATokenRealmCannotDowngradeToPlainHTTP(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, from, realm string
		refused           bool
	}{
		{
			name: "https registry, http realm", refused: true,
			from: "https://registry.example/v2/", realm: "http://auth.example/token",
		},
		{
			name: "https registry, https realm elsewhere",
			from: "https://registry.example/v2/", realm: "https://auth.example/token",
		},
		{name: "plain registry, plain realm", from: "http://127.0.0.1:5000/v2/", realm: "http://127.0.0.1:5000/token"},
		{
			name: "a realm that is not a URL", refused: true,
			from: "https://registry.example/v2/", realm: "ftp://auth.example/token",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := tokenEndpoint(`Bearer realm="`+tc.realm+`",service="registry"`, tc.from)
			if tc.refused && err == nil {
				t.Errorf("a challenge from %s sent credentials to %s", tc.from, tc.realm)
			}

			if !tc.refused && err != nil {
				t.Errorf("a challenge from %s naming %s was refused: %v", tc.from, tc.realm, err)
			}
		})
	}
}
