package analyzer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/EarthBuild/earthbuild/internal/earthfile/analyzer"
)

// cursorSource splits a fixture on the "|" cursor marker and returns the
// source without the marker plus the byte offset it occupied.
func cursorSource(t *testing.T, marked string) (string, int) {
	t.Helper()

	offset := strings.Index(marked, "|")
	require.NotEqual(t, -1, offset, "fixture must contain a | cursor marker")
	require.Equal(t, -1, strings.Index(marked[offset+1:], "|"), "fixture must contain exactly one | cursor marker")

	return marked[:offset] + marked[offset+1:], offset
}

func TestCompletionContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		kind    analyzer.CompletionKind
		prefix  string
		project string
		target  string
		command string
		scope   string
		// replace is the text the completion is expected to overwrite.
		replace string
	}{
		{
			name:    "command at start of indented line",
			source:  "VERSION 0.8\n\nbuild:\n\t|\n",
			kind:    analyzer.CompletionCommand,
			replace: "",
			scope:   "build",
		},
		{
			name:    "partial command keyword",
			source:  "VERSION 0.8\n\nbuild:\n\tRU|\n",
			kind:    analyzer.CompletionCommand,
			prefix:  "RU",
			replace: "RU",
			scope:   "build",
		},
		{
			name:    "command in the base recipe is unindented",
			source:  "VERSION 0.8\nFRO|\n",
			kind:    analyzer.CompletionCommand,
			prefix:  "FRO",
			replace: "FRO",
		},
		{
			name:    "local target after plus",
			source:  "VERSION 0.8\n\nbuild:\n\tBUILD +|\n",
			kind:    analyzer.CompletionTarget,
			command: "BUILD",
			replace: "",
			scope:   "build",
		},
		{
			name:    "partial local target",
			source:  "VERSION 0.8\n\nbuild:\n\tBUILD +te|\n",
			kind:    analyzer.CompletionTarget,
			command: "BUILD",
			prefix:  "te",
			replace: "te",
			scope:   "build",
		},
		{
			name:    "imported function after alias",
			source:  "VERSION 0.8\nIMPORT ./other AS other\n\nbuild:\n\tDO other+|\n",
			kind:    analyzer.CompletionTarget,
			command: "DO",
			project: "other",
			replace: "",
			scope:   "build",
		},
		{
			name:    "target in a relative path reference",
			source:  "VERSION 0.8\n\nbuild:\n\tBUILD ./sub+|\n",
			kind:    analyzer.CompletionTarget,
			command: "BUILD",
			project: "./sub",
			replace: "",
			scope:   "build",
		},
		{
			name:    "artifact after target slash",
			source:  "VERSION 0.8\n\nbuild:\n\tCOPY +deps/|\n",
			kind:    analyzer.CompletionArtifact,
			project: "",
			target:  "deps",
			command: "COPY",
			replace: "",
			scope:   "build",
		},
		{
			name:    "flag after double dash",
			source:  "VERSION 0.8\n\nbuild:\n\tRUN --|\n",
			kind:    analyzer.CompletionFlag,
			command: "RUN",
			replace: "--",
			scope:   "build",
		},
		{
			name:    "partial flag",
			source:  "VERSION 0.8\n\nbuild:\n\tRUN --pu|\n",
			kind:    analyzer.CompletionFlag,
			command: "RUN",
			prefix:  "pu",
			replace: "--pu",
			scope:   "build",
		},
		{
			name:    "flag on a two word command",
			source:  "VERSION 0.8\n\nbuild:\n\tSAVE ARTIFACT --|\n",
			kind:    analyzer.CompletionFlag,
			command: "SAVE ARTIFACT",
			replace: "--",
			scope:   "build",
		},
		{
			name:   "no completion inside a comment",
			source: "VERSION 0.8\n\nbuild:\n\t# build the |\n",
			kind:   analyzer.CompletionNone,
			scope:  "build",
		},
		{
			name:    "no completion in a target declaration",
			source:  "VERSION 0.8\n\nbui|\n",
			kind:    analyzer.CompletionCommand,
			prefix:  "bui",
			replace: "bui",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source, offset := cursorSource(t, test.source)
			doc := analyzer.Analyze("Earthfile", source)
			ctx := doc.CompletionContext(offset)

			require.Equal(t, test.kind, ctx.Kind, "kind")
			require.Equal(t, test.prefix, ctx.Prefix, "prefix")
			require.Equal(t, test.project, ctx.Project, "project")
			require.Equal(t, test.target, ctx.Target, "target")
			require.Equal(t, test.command, ctx.Command, "command")
			require.Equal(t, test.scope, ctx.Scope, "scope")

			if test.kind != analyzer.CompletionNone {
				require.Equal(t, test.replace, source[ctx.Replace.Start:ctx.Replace.End], "replaced text")
			}
		})
	}
}

func TestCompletionContextOnInvalidBuffer(t *testing.T) {
	t.Parallel()

	// A dangling "+" makes the reference unindexable, which is exactly when
	// completion runs. The recovery index must still supply the scope.
	source, offset := cursorSource(t, "VERSION 0.8\n\nbuild:\n\tRUN echo hi\n\ndeploy:\n\tBUILD +|\n")

	doc := analyzer.Analyze("Earthfile", source)
	require.Empty(t, doc.References, "an incomplete reference is not indexable")

	ctx := doc.CompletionContext(offset)
	require.Equal(t, analyzer.CompletionTarget, ctx.Kind)
	require.Equal(t, "deploy", ctx.Scope)
	require.Equal(t, "BUILD", ctx.Command)
}

func TestCompletionContextOutOfRangeOffsets(t *testing.T) {
	t.Parallel()

	doc := analyzer.Analyze("Earthfile", "VERSION 0.8\n")

	for _, offset := range []int{-1, len(doc.Text) + 1} {
		require.Equal(t, analyzer.CompletionNone, doc.CompletionContext(offset).Kind,
			"offset %d must not panic or classify", offset)
	}
}

func TestCompletionContextAtEndOfFileWithoutNewline(t *testing.T) {
	t.Parallel()

	source, offset := cursorSource(t, "VERSION 0.8\n\nbuild:\n\tRU|")

	ctx := analyzer.Analyze("Earthfile", source).CompletionContext(offset)
	require.Equal(t, analyzer.CompletionCommand, ctx.Kind)
	require.Equal(t, "RU", ctx.Prefix)
}

func TestCompletionContextIgnoresQuotedHash(t *testing.T) {
	t.Parallel()

	// A "#" inside a quoted argument does not start a comment, so the line is
	// still completable after it.
	source, offset := cursorSource(t, "VERSION 0.8\n\nbuild:\n\tRUN echo \"a#b\" --|\n")

	ctx := analyzer.Analyze("Earthfile", source).CompletionContext(offset)
	require.Equal(t, analyzer.CompletionFlag, ctx.Kind)
	require.Equal(t, "RUN", ctx.Command)
}
