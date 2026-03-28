package variables_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/ast/spec"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/variables"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestCollection(t *testing.T) {
	t.Parallel()

	type testCtx struct {
		coll     *variables.Collection
		features *features.Features
	}

	registerBaseSpecs := func(t *testing.T, getCtx func(t *testing.T) testCtx) {
		t.Helper()

		t.Run("builtins are used for newly registered variables", func(t *testing.T) {
			tc := getCtx(t)
			name := "EARTHLY_VERSION"
			_, ok := tc.coll.Get(name, variables.WithActive())
			require.False(t, ok)

			_, _, err := tc.coll.DeclareVar("EARTHLY_VERSION", variables.AsArg())
			require.NoError(t, err)

			v, ok := tc.coll.Get(name, variables.WithActive())
			require.True(t, ok)
			require.Equal(t, "some version", v)
		})
	}

	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()

		getCtx := func(t *testing.T) testCtx {
			t.Helper()

			f, _, err := features.Get(&spec.Version{Args: []string{"0.7"}})
			require.NoError(t, err)
			_, err = f.ProcessFlags()
			require.NoError(t, err)

			tc := testCtx{features: f}
			tc.coll = variables.NewCollection(variables.NewCollectionOpt{
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
				Features: tc.features,
			})

			return tc
		}

		registerBaseSpecs(t, getCtx)
	})

	t.Run("ArgScopeSet", func(t *testing.T) {
		t.Parallel()

		getCtx := func(t *testing.T) testCtx {
			t.Helper()

			f, _, err := features.Get(&spec.Version{Args: []string{"0.7"}})
			require.NoError(t, err)
			_, err = f.ProcessFlags()
			require.NoError(t, err)

			tc := testCtx{features: f}
			tc.features.ArgScopeSet = true
			tc.coll = variables.NewCollection(variables.NewCollectionOpt{
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
				Features: tc.features,
			})

			return tc
		}

		registerBaseSpecs(t, getCtx)

		t.Run("non-ARG variables ignore builtin values", func(t *testing.T) {
			t.Parallel()

			tc := getCtx(t)
			name := "EARTHLY_VERSION"
			_, ok := tc.coll.Get(name, variables.WithActive())
			require.False(t, ok)

			_, _, err := tc.coll.DeclareVar("EARTHLY_VERSION")
			require.NoError(t, err)

			v, ok := tc.coll.Get(name, variables.WithActive())

			require.True(t, ok)
			require.Empty(t, v)
		})
	})
}
