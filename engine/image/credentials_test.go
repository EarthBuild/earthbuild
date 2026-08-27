package image

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/types"
)

// Docker stores a Hub login under `https://index.docker.io/v1/`, and the key it
// derives that from is `docker.io`. This engine talks to `registry-1.docker.io`,
// which docker's own mapping does not recognise - so asking under the host we
// dial silently misses a `docker login` that plainly happened.
func TestAuthHostMapsDockerHubToTheKeyDockerStoresItUnder(t *testing.T) {
	t.Parallel()

	if got := authHost("registry-1.docker.io"); got != "docker.io" {
		t.Errorf("registry-1.docker.io resolved as %q, which is not where docker keeps it", got)
	}

	for _, host := range []string{"ghcr.io", "quay.io", "registry.gitlab.com", "localhost:5000"} {
		if got := authHost(host); got != host {
			t.Errorf("authHost(%q) = %q, want it left alone", host, got)
		}
	}
}

// A registry nobody logged in to is the case this engine has always had, and it
// must stay exactly as it was: no header, no failure, no prompt.
func TestNoCredentialLeavesTheRequestAnonymous(t *testing.T) {
	t.Parallel()

	var sawAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"token":"anon"}`))
	}))
	defer srv.Close()

	tok, err := fetchTokenAs(context.Background(), srv.Client(), srv.URL, credential{})
	if err != nil {
		t.Fatal(err)
	}

	if tok != "anon" {
		t.Errorf("token = %q", tok)
	}

	if sawAuth != "" {
		t.Errorf("an anonymous fetch sent Authorization: %q", sawAuth)
	}
}

// The whole point: a credential reaches the token exchange, which is the only
// request in the dance that can carry one.
func TestACredentialIsSentToTheTokenEndpoint(t *testing.T) {
	t.Parallel()

	var user, pass string
	var ok bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok = r.BasicAuth()
		_, _ = w.Write([]byte(`{"token":"private"}`))
	}))
	defer srv.Close()

	tok, err := fetchTokenAs(context.Background(), srv.Client(), srv.URL,
		credential{User: "gilescope", Secret: "hunter2"})
	if err != nil {
		t.Fatal(err)
	}

	if !ok {
		t.Fatal("no basic auth was sent, so a private image stays unreachable")
	}

	if user != "gilescope" || pass != "hunter2" {
		t.Errorf("sent %q/%q", user, pass)
	}

	if tok != "private" {
		t.Errorf("token = %q", tok)
	}
}

// A credential must never be written to the build's output. The token endpoint
// is printed in the "not pinned" note on failure, so anything folded into that
// URL would be published by a routine diagnostic.
func TestTheCredentialIsNotPutInTheURL(t *testing.T) {
	t.Parallel()

	var asked string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.String()
		_, _ = w.Write([]byte(`{"token":"x"}`))
	}))
	defer srv.Close()

	_, err := fetchTokenAs(context.Background(), srv.Client(), srv.URL,
		credential{User: "gilescope", Secret: "hunter2"})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(asked, "hunter2") || strings.Contains(asked, "gilescope") {
		t.Fatalf("the credential reached the URL: %s", asked)
	}
}

// Two users against one endpoint must not share a held token, or the second
// build is handed the first one's access.
func TestHeldTokensAreKeyedByWhoAskedForThem(t *testing.T) {
	t.Parallel()

	anon := credential{}
	mine := credential{User: "gilescope", Secret: "hunter2"}

	if anon.holdKey("https://auth.example/token") == mine.holdKey("https://auth.example/token") {
		t.Error("an anonymous token and an authenticated one share a cache key")
	}

	other := credential{User: "someone-else", Secret: "hunter2"}
	if mine.holdKey("https://auth.example/token") == other.holdKey("https://auth.example/token") {
		t.Error("two users share a cache key")
	}
}

// An identity token is an OAuth2 refresh token and needs a POST exchange this
// does not implement. Saying so beats sending it as a password and reporting
// whatever the registry makes of that.
func TestAnIdentityTokenIsReportedRatherThanMisused(t *testing.T) {
	t.Parallel()

	c := credentialFrom(types.AuthConfig{IdentityToken: "eyJ-refresh"})
	if c.Secret == "eyJ-refresh" {
		t.Fatal("an identity token was passed off as a password")
	}

	if !c.IdentityOnly {
		t.Fatal("an identity-token-only credential is not flagged as one")
	}
}

// **The credential is chosen by the registry, never by the realm.** A registry
// answers the challenge, and the challenge names the realm - so a hostile
// registry that replied `realm="https://collector.example/"` would otherwise
// choose which credential this machine hands over. Deciding from the host the
// manifest is being fetched from means the worst such a registry can do is
// receive the credential its own user already gave it.
func TestTheCredentialFollowsTheRegistryAndNotTheRealm(t *testing.T) {
	t.Parallel()

	credentials.Store("registry.example", credential{User: "right", Secret: "x"})
	credentials.Store("collector.example", credential{User: "wrong", Secret: "y"})

	got := credentialForURL("https://registry.example/v2/thing/manifests/latest")
	if got.User != "right" {
		t.Fatalf("credential came from %q, not the registry being fetched from", got.User)
	}
}

// A URL that does not parse is a machine with no credential, not a panic and
// not somebody else's.
func TestAnUnparseableURLYieldsNoCredential(t *testing.T) {
	t.Parallel()

	if got := credentialForURL("://not a url"); !got.empty() {
		t.Errorf("got %+v, want nothing", got)
	}
}

// The whole chain, against docker's own resolution rather than a stand-in: a
// config holding a Hub login under the canonical key must answer when this
// engine asks about the host it actually dials.
//
// This is the bug the mapping exists to prevent, and it is invisible to every
// other test here - `authHost` could be correct and `GetAuthConfig` still be
// asked the wrong question, or the reverse.
func TestAHubLoginIsFoundUnderTheHostWeDial(t *testing.T) {
	t.Parallel()

	cfg := &configfile.ConfigFile{
		AuthConfigs: map[string]types.AuthConfig{
			"https://index.docker.io/v1/": {Username: "hubuser", Password: "hubpass"},
		},
	}

	got := lookupIn(cfg, dockerHubHost)
	if got.User != "hubuser" || got.Secret != "hubpass" {
		t.Fatalf("a Hub login was not found from %s: %+v", dockerHubHost, got)
	}
}

// A registry filed under its own name is the ordinary case and must not be
// disturbed by the Hub special case.
func TestAnOrdinaryRegistryIsFoundUnderItsOwnName(t *testing.T) {
	t.Parallel()

	cfg := &configfile.ConfigFile{
		AuthConfigs: map[string]types.AuthConfig{
			"ghcr.io": {Username: "gh", Password: "pat"},
		},
	}

	if got := lookupIn(cfg, "ghcr.io"); got.User != "gh" || got.Secret != "pat" {
		t.Fatalf("ghcr.io credential not found: %+v", got)
	}
}

// A machine logged in to one registry must not present that credential to
// another. Obvious, and exactly the sort of thing a keying change breaks.
func TestALoginDoesNotLeakToADifferentRegistry(t *testing.T) {
	t.Parallel()

	cfg := &configfile.ConfigFile{
		AuthConfigs: map[string]types.AuthConfig{
			"ghcr.io": {Username: "gh", Password: "pat"},
		},
	}

	if got := lookupIn(cfg, "quay.io"); !got.empty() {
		t.Fatalf("quay.io was handed ghcr.io's credential: %+v", got)
	}
}

// withConfig puts a config in front of the real one for the duration of a test,
// and clears the per-host memo either side so tests cannot leak into each other.
func withConfig(t *testing.T, cfg *configfile.ConfigFile) {
	t.Helper()

	was := dockerConfig
	dockerConfig = func() *configfile.ConfigFile { return cfg }
	credentials.Clear()

	t.Cleanup(func() {
		dockerConfig = was
		credentials.Clear()
	})
}

// The dance end to end, against a registry that refuses anonymously: challenge,
// realm, credential, token. This is the path a private image takes, and the one
// that was missing entirely - a unit test of each part passes without it.
//
// Not parallel: it stands a config in front of a package variable.
//
//nolint:paralleltest // stands a config in front of a package variable
func TestAPrivateRegistryIsReachedWithAStoredLogin(t *testing.T) {
	mux := http.NewServeMux()

	var issued bool

	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "u" || pass != "p" {
			w.WriteHeader(http.StatusForbidden)

			return
		}

		issued = true

		_, _ = w.Write([]byte(`{"token":"granted"}`))
	})

	mux.HandleFunc("/v2/private/thing/manifests/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate",
			`Bearer realm="`+srv.URL+`/token",service="reg",scope="repository:private/thing:pull"`)
		w.WriteHeader(http.StatusUnauthorized)
	})

	host := strings.TrimPrefix(srv.URL, "http://")
	withConfig(t, &configfile.ConfigFile{
		AuthConfigs: map[string]types.AuthConfig{host: {Username: "u", Password: "p"}},
	})

	tok, err := token(context.Background(), srv.Client(),
		srv.URL+"/v2/private/thing/manifests/latest", t.TempDir(), "private/thing")
	if err != nil {
		t.Fatalf("a stored login did not reach the registry: %v", err)
	}

	if !issued {
		t.Fatal("the token endpoint was never satisfied")
	}

	if tok != "granted" {
		t.Errorf("token = %q", tok)
	}
}

// The same registry with nothing stored must fail, or the test above proves
// only that the server is generous.
//
//nolint:paralleltest // stands a config in front of a package variable
func TestTheSameRegistryRefusesWithoutALogin(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)

	defer srv.Close()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); !ok {
			w.WriteHeader(http.StatusForbidden)

			return
		}

		_, _ = w.Write([]byte(`{"token":"granted"}`))
	})

	mux.HandleFunc("/v2/private/thing/manifests/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate",
			`Bearer realm="`+srv.URL+`/token",service="reg",scope="repository:private/thing:pull"`)
		w.WriteHeader(http.StatusUnauthorized)
	})

	withConfig(t, &configfile.ConfigFile{AuthConfigs: map[string]types.AuthConfig{}})

	_, err := token(context.Background(), srv.Client(),
		srv.URL+"/v2/private/thing/manifests/latest", t.TempDir(), "private/thing")
	if err == nil {
		t.Fatal("an anonymous fetch was accepted, so the test above proves nothing")
	}

	if !strings.Contains(err.Error(), "no credential was presented") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
}

// A self-hosted registry on a non-default port is filed under `host:port`, so
// dropping the port looks up a name nothing was stored under. This failed
// before `credentialForURL` used `Host` rather than `Hostname`.
func TestAPortIsPartOfTheRegistryName(t *testing.T) {
	t.Parallel()

	cfg := &configfile.ConfigFile{
		AuthConfigs: map[string]types.AuthConfig{
			"localhost:5000": {Username: "local", Password: "pw"},
		},
	}

	if got := lookupIn(cfg, "localhost:5000"); got.User != "local" {
		t.Fatalf("a registry on a port was not found: %+v", got)
	}
}
