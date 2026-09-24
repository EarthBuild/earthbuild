package exec

import (
	"slices"
	"testing"
)

// Every AWS variable reaches the step; only the credentials are scanned for.
//
// **The distinction is the whole safety of the feature.** A secret registered
// with the scanner is redacted from the build log and *fails the build* if it
// appears in a layer, and the scanner matches literal values with no length or
// entropy guard. Register `AWS_DEFAULT_REGION=us-east-1` and every layer
// containing the string `us-east-1` - a config file, a README, an SDK default -
// fails as a credential leak.
//
// So the region, the profile and the endpoint travel as ordinary environment,
// and only the four that actually authorise anything are named as secrets.
func TestOnlyTheAWSCredentialsAreTreatedAsSecrets(t *testing.T) {
	t.Parallel()

	env, secret := awsEnv(map[string]string{
		"AWS_ACCESS_KEY_ID":     "AKIAIOSFODNN7EXAMPLE",
		"AWS_SECRET_ACCESS_KEY": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"AWS_SESSION_TOKEN":     "FwoGZXIvYXdzEExampleToken",
		"AWS_DEFAULT_REGION":    "us-east-1",
		"AWS_PROFILE":           "build",
	})

	// Everything reaches the step: an SDK that cannot find its region is no
	// more use than one that cannot find its key.
	for _, want := range []string{
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"AWS_DEFAULT_REGION=us-east-1",
		"AWS_PROFILE=build",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("the step's environment lacks %q\n  got %v", want, env)
		}
	}

	// Sorted, because two builds that differ only in map iteration order are
	// two builds this engine cannot tell apart.
	if !slices.IsSorted(env) {
		t.Errorf("the environment is not in a settled order: %v", env)
	}

	wantSecret := []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"}
	if !slices.Equal(secret, wantSecret) {
		t.Errorf("scanned %v, want %v", secret, wantSecret)
	}

	for _, never := range []string{"AWS_DEFAULT_REGION", "AWS_PROFILE"} {
		if slices.Contains(secret, never) {
			t.Errorf("%s is registered as a secret"+
				"\n  the scanner has no length or entropy guard, so any layer"+
				" containing its value would fail the build as a leak", never)
		}
	}
}

// Nothing to forward is not the same as forwarding nothing.
func TestNoAWSCredentialsForwardsNothing(t *testing.T) {
	t.Parallel()

	env, secret := awsEnv(nil)
	if env != nil || secret != nil {
		t.Errorf("awsEnv(nil) = (%v, %v), want (nil, nil)", env, secret)
	}
}
