package guest

import (
	"strings"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// secretsFrom is every credential this step was given, by a name safe to print.
//
// **Two kinds, told apart two ways.** A secret mount carries its value in the
// request and is named by where it appears; a secret environment variable is one
// entry of Env among many and is identifiable only because the host says which
// names are secret.
//
// A value that is empty is dropped rather than gathered: an empty string appears
// in every file, so a secret nobody supplied would report the whole layer.
func secretsFrom(req Request) []layer.Secret {
	var out []layer.Secret

	for _, m := range req.Mounts {
		if m.Secret != "" {
			out = append(out, layer.Secret{Name: m.Target, Value: m.Secret})
		}
	}

	if len(req.SecretEnv) == 0 {
		return out
	}

	want := make(map[string]bool, len(req.SecretEnv))
	for _, name := range req.SecretEnv {
		want[name] = true
	}

	for _, kv := range req.Env {
		name, value, ok := strings.Cut(kv, "=")
		if ok && want[name] && value != "" {
			out = append(out, layer.Secret{Name: name, Value: value})
		}
	}

	return out
}
