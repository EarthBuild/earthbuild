package subcmd

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/stretchr/testify/require"
)

func TestParseDocTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		wantTarget  string
		wantErrLike string
		wantSingle  bool
	}{
		{name: "empty documents all base targets", path: "", wantTarget: docBaseTarget, wantSingle: false},
		{name: "local dir documents all base targets", path: ".", wantTarget: docBaseTarget, wantSingle: false},
		{name: "explicit target is single", path: "+build", wantTarget: "+build", wantSingle: true},
		{name: "explicit base target is single", path: "+base", wantTarget: "+base", wantSingle: true},
		{name: "pathed target is single", path: "./foo+build", wantTarget: "./foo+build", wantSingle: true},
		{name: "remote path rejected", path: "github.com/foo/bar+x", wantErrLike: "remote-paths are not currently supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target, single, err := parseDocTarget(tt.path)
			if tt.wantErrLike != "" {
				require.ErrorContains(t, err, tt.wantErrLike)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantTarget, target.String())
			require.Equal(t, tt.wantSingle, single)
		})
	}
}

func TestDocTargetFixtures(t *testing.T) {
	t.Parallel()

	ef, ftrs := parseDocFixture(t, "target-docs.earth")

	t.Run("documented target", func(t *testing.T) {
		t.Parallel()

		tgt := mustFindDocTarget(t, ef, "documented-target")
		out, err := captureDoc(func(d *Doc) error {
			return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, false)
		})
		require.NoError(t, err)

		require.Contains(t, out, "+documented-target\n")
		require.Contains(t, out, "documented-target is a target with documentation\n")
		require.Contains(t, out, "that spans multiple lines.\n")
		require.Contains(t, out, "It also has a separator between paragraphs.\n")
	})

	t.Run("undocumented target fails", func(t *testing.T) {
		t.Parallel()

		tgt := mustFindDocTarget(t, ef, "undocumented-target")
		err := (&Doc{}).documentSingleTarget(io.Discard, "", ftrs, ef.BaseRecipe, tgt, false)
		require.Error(t, err)
		require.ErrorContains(t, err, "no doc comment found")
		require.ErrorIs(t, err, errNoDocComment)
	})

	t.Run("incorrectly documented target fails", func(t *testing.T) {
		t.Parallel()

		tgt := mustFindDocTarget(t, ef, "incorrectly-documented-target")
		err := (&Doc{}).documentSingleTarget(io.Discard, "", ftrs, ef.BaseRecipe, tgt, false)
		require.Error(t, err)
		require.ErrorContains(t, err, "no doc comment found")
		require.ErrorIs(t, err, errNoDocComment)
	})
}

func TestDocRecipeBlockFixture(t *testing.T) {
	t.Parallel()

	ef, ftrs := parseDocFixture(t, "doc-recipe-block.earth")
	tgt := mustFindDocTarget(t, ef, "foo")

	td, err := parseDocSections(ftrs, ef.BaseRecipe, tgt.Recipe, false)
	require.NoError(t, err)
	require.Len(t, td.args, 5)
	require.Equal(t, "globalArg", td.args[0].Name)
	require.True(t, td.args[0].Global)
	require.NotEmpty(t, td.args[0].Description)
	require.Equal(t, "withDefault", td.args[1].Name)
	require.Equal(t, "foo", *td.args[1].DefaultVal)
	require.NotEmpty(t, td.args[1].Description)
	require.Equal(t, "withDocs", td.args[2].Name)
	require.NotEmpty(t, td.args[2].Description)
	require.Equal(t, "withoutDocs", td.args[3].Name)
	require.Empty(t, td.args[3].Description)
	require.Equal(t, "requiredArg", td.args[4].Name)
	require.True(t, td.args[4].Required)

	require.Equal(t, []string{"bar.txt", "baz.txt"}, docIdentifiers(td.artifacts))
	require.Equal(
		t,
		[]string{"baz.txt -> out/baz.txt", "bacon.txt -> out/eggs.txt"},
		docIdentifiers(td.localArtifacts),
	)
	require.Equal(t, []string{"baz", "bar", "bacon, eggs"}, docIdentifiers(td.images))
	require.NotEmpty(t, td.artifacts[0].body)
	require.Empty(t, td.artifacts[1].body)
	require.NotEmpty(t, td.localArtifacts[0].body)
	require.NotEmpty(t, td.images[0].body)
	require.Empty(t, td.images[1].body)

	out, err := captureDoc(func(d *Doc) error {
		return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, true)
	})
	require.NoError(t, err)

	require.Contains(t, out, "+foo\n")
	require.Contains(t, out, "ARG")
	require.Contains(t, out, "DEFAULT")
	require.Contains(t, out, "DESCRIPTION")
	require.Contains(t, out, "--requiredArg (required)")
	require.Contains(t, out, "--globalArg")
	require.Contains(t, out, "--withDefault")
	require.Contains(t, out, "ARTIFACTS:")
	require.Contains(t, out, "LOCAL ARTIFACTS:")
	require.Contains(t, out, "IMAGES:")
}

func TestDocArgDescription(t *testing.T) {
	t.Parallel()

	content := `VERSION 0.8

# build documents the build target
build:
    ARG --description="Environment stage (dev, staging, prod)" ENV=prod
    ARG --required --description="Database connection URL" DB_URL

    FROM alpine:3.18
    RUN echo "Building for ${ENV}"
`
	ef, err := earthfile.Parse("Earthfile", content, earthfile.WithSourceMap())
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	tgt := mustFindDocTarget(t, ef, "build")

	td, err := parseDocSections(ftrs, ef.BaseRecipe, tgt.Recipe, false)
	require.NoError(t, err)
	require.Len(t, td.args, 2)
	require.Equal(t, "ENV", td.args[0].Name)
	require.Equal(t, "prod", *td.args[0].DefaultVal)
	require.Equal(t, "Environment stage (dev, staging, prod)", td.args[0].Description)
	require.False(t, td.args[0].Required)
	require.Equal(t, "DB_URL", td.args[1].Name)
	require.Nil(t, td.args[1].DefaultVal)
	require.Equal(t, "Database connection URL", td.args[1].Description)
	require.True(t, td.args[1].Required)

	out, err := captureDoc(func(d *Doc) error {
		return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, false)
	})
	require.NoError(t, err)

	require.Contains(t, out, "+build\n")
	require.Contains(t, out, "ARG")
	require.Contains(t, out, "DEFAULT")
	require.Contains(t, out, "DESCRIPTION")
	require.Contains(t, out, "--DB_URL (required)")
	require.Contains(t, out, "Database connection URL")
	require.Contains(t, out, "--ENV")
	require.Contains(t, out, "prod")
	require.Contains(t, out, "Environment stage (dev, staging, prod)")
}

func TestDocArgMultilineDescriptionParagraphs(t *testing.T) {
	t.Parallel()

	content := `VERSION 0.8

# deploy documents deployment
deploy:
    # DB_URL is the database connection URL.
    #
    # It must be formatted as postgres://user:pass@host:port/db
    # and SSL must be enabled.
    ARG --required DB_URL

    FROM alpine:3.18
    RUN echo "$DB_URL"
`
	ef, err := earthfile.Parse("Earthfile", content, earthfile.WithSourceMap())
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	tgt := mustFindDocTarget(t, ef, "deploy")

	out, err := captureDoc(func(d *Doc) error {
		return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, false)
	})
	require.NoError(t, err)

	expected := "  --DB_URL (required)           DB_URL is the database connection URL.\n" +
		"\n" +
		"                                It must be formatted as postgres://user:pass@host:port/db\n" +
		"                                and SSL must be enabled.\n"
	require.Contains(t, out, expected)
}

func TestDocBaseTarget(t *testing.T) {
	t.Parallel()

	content := `VERSION 0.8

ARG --description="Container Registry organisation" CR_ORG="earthbuild"
ARG --description="Container Registry repository" CR_REPO="earthbuild"
ARG --description="Registry base URL" REGISTRY_BASE="ghcr.io"
ARG --global IMAGE_REGISTRY=$REGISTRY_BASE/$CR_ORG/$CR_REPO
`
	ef, err := earthfile.Parse("Earthfile", content, earthfile.WithSourceMap())
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	tgt := mustFindDocTarget(t, ef, "base")
	require.Equal(t, earthfile.TargetBase, tgt.Name)

	td, err := parseDocSections(ftrs, ef.BaseRecipe, tgt.Recipe, true)
	require.NoError(t, err)
	require.Len(t, td.args, 4)
	require.Equal(t, "CR_ORG", td.args[0].Name)
	require.Equal(t, `"earthbuild"`, *td.args[0].DefaultVal)
	require.Equal(t, "Container Registry organisation", td.args[0].Description)
	require.Equal(t, "CR_REPO", td.args[1].Name)
	require.Equal(t, "REGISTRY_BASE", td.args[2].Name)
	require.Equal(t, "IMAGE_REGISTRY", td.args[3].Name)

	out, err := captureDoc(func(d *Doc) error {
		return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, false)
	})
	require.NoError(t, err)

	require.Contains(t, out, "+base\n")
	require.Contains(t, out, "ARG")
	require.Contains(t, out, "DEFAULT")
	require.Contains(t, out, "DESCRIPTION")
	require.Contains(t, out, "--CR_ORG")
	require.Contains(t, out, "earthbuild")
	require.Contains(t, out, "Container Registry organisation")
	require.Contains(t, out, "--CR_REPO")
	require.Contains(t, out, "Container Registry repository")
	require.Contains(t, out, "--REGISTRY_BASE")
	require.Contains(t, out, "Registry base URL")
}

func TestDocEmptyLineBetweenTargets(t *testing.T) {
	t.Parallel()

	content := `VERSION 0.8

# base contains shared setup
ARG FOO=bar

# alpha is the first target
alpha:
    FROM alpine:3.18

# bravo is the second target
bravo:
    FROM alpine:3.18
`
	ef, err := earthfile.Parse("Earthfile", content, earthfile.WithSourceMap())
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	tgts := make([]earthfile.Target, 0, len(ef.Targets)+1)
	if len(ef.BaseRecipe) > 0 {
		tgts = append(tgts, makeBaseTarget(ef))
	}

	tgts = append(tgts, ef.Targets...)

	out, err := captureDoc(func(d *Doc) error {
		fmt.Fprintln(d.writer(), "TARGETS:")

		var documentedCount int

		w := d.writer()

		for _, tgt := range tgts {
			var buf bytes.Buffer

			docErr := d.documentSingleTarget(&buf, "  ", ftrs, ef.BaseRecipe, tgt, false)
			if docErr != nil {
				continue
			}

			if documentedCount > 0 {
				fmt.Fprintln(w)
			}

			documentedCount++

			_, copyErr := io.Copy(w, &buf)
			if copyErr != nil {
				return copyErr
			}
		}

		return nil
	})
	require.NoError(t, err)

	expected := `TARGETS:
  +base
      base contains shared setup

    ARG    DEFAULT  DESCRIPTION
    --FOO  bar

  +alpha
      alpha is the first target

  +bravo
      bravo is the second target
`
	require.Equal(t, expected, out)
}

func TestDocUndocumentedBaseTargetSkipped(t *testing.T) {
	t.Parallel()

	content := `VERSION 0.8

ARG FOO=bar

# alpha is the first target
alpha:
    FROM alpine:3.18
`
	ef, err := earthfile.Parse("Earthfile", content, earthfile.WithSourceMap())
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	tgts := make([]earthfile.Target, 0, len(ef.Targets)+1)
	if len(ef.BaseRecipe) > 0 {
		tgts = append(tgts, makeBaseTarget(ef))
	}

	tgts = append(tgts, ef.Targets...)

	out, err := captureDoc(func(d *Doc) error {
		fmt.Fprintln(d.writer(), "TARGETS:")

		var documentedCount int

		w := d.writer()

		for _, tgt := range tgts {
			var buf bytes.Buffer

			docErr := d.documentSingleTarget(&buf, "  ", ftrs, ef.BaseRecipe, tgt, false)
			if docErr != nil {
				continue
			}

			if documentedCount > 0 {
				fmt.Fprintln(w)
			}

			documentedCount++

			_, copyErr := io.Copy(w, &buf)
			if copyErr != nil {
				return copyErr
			}
		}

		return nil
	})
	require.NoError(t, err)

	require.NotContains(t, out, "+base")
	require.Contains(t, out, "+alpha")
}

func TestDocColorOutput(t *testing.T) {
	t.Parallel()

	content := `VERSION 0.8

# build documents the build target
build:
    ARG --description="Environment stage" ENV=prod
    ARG --required --description="Database connection URL" DB_URL
`
	ef, err := earthfile.Parse("Earthfile", content, earthfile.WithSourceMap())
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	tgt := mustFindDocTarget(t, ef, "build")

	t.Run("plain output by default when buffer", func(t *testing.T) {
		t.Parallel()

		out, err := captureDoc(func(d *Doc) error {
			return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, false)
		})
		require.NoError(t, err)
		require.NotContains(t, out, "\x1b[")
	})

	t.Run("colored output when forceColor is enabled", func(t *testing.T) {
		t.Parallel()

		out, err := captureDoc(func(d *Doc) error {
			d.forceColor = true

			return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, false)
		})
		require.NoError(t, err)

		require.Contains(t, out, "\x1b[1;36m+build\x1b[")
		require.Contains(t, out, "\x1b[1mARG\x1b[")
		require.Contains(t, out, "\x1b[1mDEFAULT\x1b[")
		require.Contains(t, out, "\x1b[1mDESCRIPTION\x1b[")
		require.Contains(t, out, "\x1b[36m--DB_URL\x1b[")
		require.Contains(t, out, "\x1b[36m--ENV\x1b[")
		require.Contains(t, out, "\x1b[33m(required)\x1b[")
	})
}

func TestDocGlobalAndRequiredBadges(t *testing.T) {
	t.Parallel()

	content := `VERSION 0.8

ARG --global --description="Container registry" REGISTRY="docker.io"
ARG --global --required --description="Registry token" REGISTRY_TOKEN
ARG --global --description="Unused global" UNUSED_GLOBAL="foo"

# deploy documents deployment
deploy:
    ARG --required --description="App version" APP_VERSION
    ARG --description="Environment" ENV=prod
    RUN echo "$REGISTRY" "$REGISTRY_TOKEN"

# status documents status
status:
    RUN echo "all good"
`
	ef, err := earthfile.Parse("Earthfile", content, earthfile.WithSourceMap())
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	tgt := mustFindDocTarget(t, ef, "deploy")
	tgtStatus := mustFindDocTarget(t, ef, "status")

	t.Run("plain output badges", func(t *testing.T) {
		t.Parallel()

		out, err := captureDoc(func(d *Doc) error {
			return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, false)
		})
		require.NoError(t, err)

		require.Contains(t, out, "--REGISTRY (global)")
		require.Contains(t, out, "--REGISTRY_TOKEN (required, global)")
		require.Contains(t, out, "--APP_VERSION (required)")
		require.Contains(t, out, "--ENV")
		require.NotContains(t, out, "--ENV (")
		require.NotContains(t, out, "UNUSED_GLOBAL")

		outStatus, err := captureDoc(func(d *Doc) error {
			return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgtStatus, false)
		})
		require.NoError(t, err)
		require.Contains(t, outStatus, "+status\n")
		require.NotContains(t, outStatus, "ARG")
		require.NotContains(t, outStatus, "REGISTRY")
		require.NotContains(t, outStatus, "UNUSED_GLOBAL")
	})

	t.Run("colored output badges", func(t *testing.T) {
		t.Parallel()

		out, err := captureDoc(func(d *Doc) error {
			d.forceColor = true

			return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, false)
		})
		require.NoError(t, err)

		require.Contains(t, out, "\x1b[33m(global)\x1b[")
		require.Contains(t, out, "\x1b[33m(required)\x1b[")
		require.Contains(t, out, "\x1b[33mrequired\x1b[")
		require.Contains(t, out, "\x1b[33mglobal\x1b[")
	})
}

func TestDocGlobalArgRunFlagsNotReferenced(t *testing.T) {
	t.Parallel()

	content := `VERSION 0.8

ARG --global --description="Quiet mode" quiet="false"
ARG --global --description="Registry tag" TAG="v1.0.0"

# build documents build
build:
    RUN git checkout --quiet main
    BUILD +sub --TAG=$TAG
`
	ef, err := earthfile.Parse("Earthfile", content, earthfile.WithSourceMap())
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	tgt := mustFindDocTarget(t, ef, "build")

	out, err := captureDoc(func(d *Doc) error {
		return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, false)
	})
	require.NoError(t, err)

	require.Contains(t, out, "--TAG (global)")
	require.NotContains(t, out, "--quiet")
}

func TestDocGlobalArgCopyParamsForm(t *testing.T) {
	t.Parallel()

	content := `VERSION 0.8

ARG --global --description="Feature flag" ENABLE_FEATURE
ARG --global --description="Unused flag" UNUSED_FLAG

# build documents build
build:
    COPY (+sub --ENABLE_FEATURE) /src /dst
`
	ef, err := earthfile.Parse("Earthfile", content, earthfile.WithSourceMap())
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	tgt := mustFindDocTarget(t, ef, "build")

	out, err := captureDoc(func(d *Doc) error {
		return d.documentSingleTarget(d.writer(), "", ftrs, ef.BaseRecipe, tgt, false)
	})
	require.NoError(t, err)

	require.Contains(t, out, "--ENABLE_FEATURE (global)")
	require.NotContains(t, out, "--UNUSED_FLAG")
}

func TestDocString(t *testing.T) {
	t.Parallel()

	const targetBuild = "build"

	tests := []struct {
		errTarget error
		name      string
		body      string
		want      string
		targets   []string
		wantErr   bool
	}{
		{
			name:    "exact match space separated",
			body:    "build is the build target\n",
			targets: []string{targetBuild},
			want:    "build is the build target\n",
		},
		{
			name:    "single word comment without space",
			body:    "build\n",
			targets: []string{targetBuild},
			want:    "build\n",
		},
		{
			name:    "tab separated comment",
			body:    "build\tdoes the build\n",
			targets: []string{targetBuild},
			want:    "build\tdoes the build\n",
		},
		{
			name:    "leading whitespace in comment",
			body:    "   build is the target\n",
			targets: []string{targetBuild},
			want:    "   build is the target\n",
		},
		{
			errTarget: errNoDocComment,
			name:      "empty comment",
			body:      "  \n\t  ",
			targets:   []string{targetBuild},
			wantErr:   true,
		},
		{
			errTarget: errNoDocComment,
			name:      "unmatched comment",
			body:      "other is not build",
			targets:   []string{targetBuild},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := docString(tt.body, tt.targets...)
			if tt.wantErr {
				require.Error(t, err)

				if tt.errTarget != nil {
					require.ErrorIs(t, err, tt.errTarget)
				}

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func parseDocFixture(t *testing.T, fixture string) (earthfile.Tree, *features.Features) {
	t.Helper()

	ef, err := earthfile.ParseFile(filepath.Join("testdata", fixture), earthfile.WithSourceMap())
	require.NoError(t, err)

	ftrs, _, err := features.Get(ef.Version)
	require.NoError(t, err)
	_, err = ftrs.ProcessFlags()
	require.NoError(t, err)

	return ef, ftrs
}

func mustFindDocTarget(t *testing.T, ef earthfile.Tree, name string) earthfile.Target {
	t.Helper()

	tgt, err := findTarget(ef, name)
	require.NoError(t, err)

	return tgt
}

func docIdentifiers(sections []docSection) []string {
	ids := make([]string, len(sections))
	for i, section := range sections {
		ids[i] = section.identifier
	}

	return ids
}

// captureDoc runs fn against a Doc that writes into an in-memory buffer,
// returning the rendered output. No global stdout hijacking, so it is safe
// under t.Parallel() and cannot deadlock on a full pipe.
func captureDoc(fn func(*Doc) error) (string, error) {
	var buf bytes.Buffer

	err := fn(&Doc{out: &buf})

	return buf.String(), err
}
