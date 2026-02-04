package variables_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/ast/spec"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/variables"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestCollection(t *testing.T) {
	t.Parallel()

	setupFeatures := func(t *testing.T) *features.Features {
		t.Helper()
		f, _, err := features.Get(&spec.Version{Args: []string{"0.7"}})
		if err != nil {
			t.Fatalf("failed to get features: %v", err)
		}
		_, err = f.ProcessFlags()
		if err != nil {
			t.Fatalf("failed to process flags: %v", err)
		}
		return f
	}

	runBaseSpecs := func(t *testing.T, coll *variables.Collection) {
		t.Helper()

		t.Run("builtins are used for newly registered variables", func(t *testing.T) {
			t.Parallel()
			name := "EARTHLY_VERSION"
			_, ok := coll.Get(name, variables.WithActive())
			if ok {
				t.Error("expected Get to return false before declaring variable")
			}

			_, _, err := coll.DeclareVar("EARTHLY_VERSION", variables.AsArg())
			if err != nil {
				t.Errorf("expected no error from DeclareVar, got: %v", err)
			}
			v, ok := coll.Get(name, variables.WithActive())
			if !ok {
				t.Error("expected Get to return true after declaring variable")
			}
			if v != "some version" {
				t.Errorf("expected value to be %q, got %q", "some version", v)
			}
		})
	}

	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()

		f := setupFeatures(t)
		coll := variables.NewCollection(variables.NewCollectionOpt{
			PlatformResolver: platutil.NewResolver(specs.Platform{
				Architecture: "foo",
				OS:           "bar",
				OSVersion:    "baz",
				OSFeatures:   []string{"stub"},
				Variant:      "bacon",
			}),
			BuiltinArgs: variables.DefaultArgs{
				EarthlyVersion: "some version",
			},
			Features: f,
		})

		runBaseSpecs(t, coll)
	})

	t.Run("ArgScopeSet", func(t *testing.T) {
		t.Parallel()

		f := setupFeatures(t)
		f.ArgScopeSet = true
		coll := variables.NewCollection(variables.NewCollectionOpt{
			PlatformResolver: platutil.NewResolver(specs.Platform{
				Architecture: "foo",
				OS:           "bar",
				OSVersion:    "baz",
				OSFeatures:   []string{"stub"},
				Variant:      "bacon",
			}),
			BuiltinArgs: variables.DefaultArgs{
				EarthlyVersion: "some version",
			},
			Features: f,
		})

		runBaseSpecs(t, coll)

		t.Run("non-ARG variables ignore builtin values", func(t *testing.T) {
			t.Parallel()

			f := setupFeatures(t)
			f.ArgScopeSet = true
			testColl := variables.NewCollection(variables.NewCollectionOpt{
				PlatformResolver: platutil.NewResolver(specs.Platform{
					Architecture: "foo",
					OS:           "bar",
					OSVersion:    "baz",
					OSFeatures:   []string{"stub"},
					Variant:      "bacon",
				}),
				BuiltinArgs: variables.DefaultArgs{
					EarthlyVersion: "some version",
				},
				Features: f,
			})

			name := "EARTHLY_VERSION"
			_, ok := testColl.Get(name, variables.WithActive())
			if ok {
				t.Error("expected Get to return false before declaring variable")
			}

			_, _, err := testColl.DeclareVar("EARTHLY_VERSION")
			if err != nil {
				t.Errorf("expected no error from DeclareVar, got: %v", err)
			}
			v, ok := testColl.Get(name, variables.WithActive())
			if !ok {
				t.Error("expected Get to return true after declaring variable")
			}
			if v != "" {
				t.Errorf("expected value to be empty string, got %q", v)
			}
		})
	})
}
