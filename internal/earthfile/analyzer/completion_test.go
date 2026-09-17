package analyzer

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
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
		kind    CompletionKind
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
			kind:    CompletionCommand,
			replace: "",
			scope:   "build",
		},
		{
			name:    "partial command keyword",
			source:  "VERSION 0.8\n\nbuild:\n\tRU|\n",
			kind:    CompletionCommand,
			prefix:  "RU",
			replace: "RU",
			scope:   "build",
		},
		{
			name:    "command in the base recipe is unindented",
			source:  "VERSION 0.8\nFRO|\n",
			kind:    CompletionCommand,
			prefix:  "FRO",
			replace: "FRO",
		},
		{
			name:    "local target after plus",
			source:  "VERSION 0.8\n\nbuild:\n\tBUILD +|\n",
			kind:    CompletionTarget,
			command: "BUILD",
			replace: "",
			scope:   "build",
		},
		{
			name:    "partial local target",
			source:  "VERSION 0.8\n\nbuild:\n\tBUILD +te|\n",
			kind:    CompletionTarget,
			command: "BUILD",
			prefix:  "te",
			replace: "te",
			scope:   "build",
		},
		{
			name:    "imported function after alias",
			source:  "VERSION 0.8\nIMPORT ./other AS other\n\nbuild:\n\tDO other+|\n",
			kind:    CompletionTarget,
			command: "DO",
			project: "other",
			replace: "",
			scope:   "build",
		},
		{
			name:    "target in a relative path reference",
			source:  "VERSION 0.8\n\nbuild:\n\tBUILD ./sub+|\n",
			kind:    CompletionTarget,
			command: "BUILD",
			project: "./sub",
			replace: "",
			scope:   "build",
		},
		{
			name:    "artifact after target slash",
			source:  "VERSION 0.8\n\nbuild:\n\tCOPY +deps/|\n",
			kind:    CompletionArtifact,
			project: "",
			target:  "deps",
			command: "COPY",
			replace: "",
			scope:   "build",
		},
		{
			name:    "flag after double dash",
			source:  "VERSION 0.8\n\nbuild:\n\tRUN --|\n",
			kind:    CompletionFlag,
			command: "RUN",
			replace: "--",
			scope:   "build",
		},
		{
			name:    "partial flag",
			source:  "VERSION 0.8\n\nbuild:\n\tRUN --pu|\n",
			kind:    CompletionFlag,
			command: "RUN",
			prefix:  "pu",
			replace: "--pu",
			scope:   "build",
		},
		{
			name:    "flag on a two word command",
			source:  "VERSION 0.8\n\nbuild:\n\tSAVE ARTIFACT --|\n",
			kind:    CompletionFlag,
			command: "SAVE ARTIFACT",
			replace: "--",
			scope:   "build",
		},
		{
			name:   "no completion inside a comment",
			source: "VERSION 0.8\n\nbuild:\n\t# build the |\n",
			kind:   CompletionNone,
			scope:  "build",
		},
		{
			name:    "no completion in a target declaration",
			source:  "VERSION 0.8\n\nbui|\n",
			kind:    CompletionCommand,
			prefix:  "bui",
			replace: "bui",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source, offset := cursorSource(t, test.source)
			doc := Analyze("Earthfile", source)
			ctx := doc.CompletionContext(offset)

			require.Equal(t, test.kind, ctx.Kind, "kind")
			require.Equal(t, test.prefix, ctx.Prefix, "prefix")
			require.Equal(t, test.project, ctx.Project, "project")
			require.Equal(t, test.target, ctx.Target, "target")
			require.Equal(t, test.command, ctx.Command, "command")
			require.Equal(t, test.scope, ctx.Scope, "scope")

			if test.kind != CompletionNone {
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

	doc := Analyze("Earthfile", source)
	require.Empty(t, doc.References, "an incomplete reference is not indexable")

	ctx := doc.CompletionContext(offset)
	require.Equal(t, CompletionTarget, ctx.Kind)
	require.Equal(t, "deploy", ctx.Scope)
	require.Equal(t, "BUILD", ctx.Command)
}

func TestCompletionContextOutOfRangeOffsets(t *testing.T) {
	t.Parallel()

	doc := Analyze("Earthfile", "VERSION 0.8\n")

	for _, offset := range []int{-1, len(doc.Text) + 1} {
		require.Equal(t, CompletionNone, doc.CompletionContext(offset).Kind,
			"offset %d must not panic or classify", offset)
	}
}

func TestCompletionContextAtEndOfFileWithoutNewline(t *testing.T) {
	t.Parallel()

	source, offset := cursorSource(t, "VERSION 0.8\n\nbuild:\n\tRU|")

	ctx := Analyze("Earthfile", source).CompletionContext(offset)
	require.Equal(t, CompletionCommand, ctx.Kind)
	require.Equal(t, "RU", ctx.Prefix)
}

func TestCompletionContextIgnoresQuotedHash(t *testing.T) {
	t.Parallel()

	// A "#" inside a quoted argument does not start a comment, so the line is
	// still completable after it.
	source, offset := cursorSource(t, "VERSION 0.8\n\nbuild:\n\tRUN echo \"a#b\" --|\n")

	ctx := Analyze("Earthfile", source).CompletionContext(offset)
	require.Equal(t, CompletionFlag, ctx.Kind)
	require.Equal(t, "RUN", ctx.Command)
}

func completionLabels(items []Completion) []string {
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}

	return labels
}

func TestCompletionsOfferCanonicalCommands(t *testing.T) {
	t.Parallel()

	source, offset := cursorSource(t, "VERSION 0.8\n\nbuild:\n\t|\n")

	items := Analyze("Earthfile", source).Completions(offset, nil)
	require.Len(t, items, len(earthfile.Commands()))
	require.Contains(t, completionLabels(items), "RUN")
	require.Contains(t, completionLabels(items), "SAVE ARTIFACT")

	for _, item := range items {
		require.Equal(t, ItemKeyword, item.Kind)
	}
}

func TestCompletionsFilterCommandsByPrefix(t *testing.T) {
	t.Parallel()

	source, offset := cursorSource(t, "VERSION 0.8\n\nbuild:\n\tCO|\n")

	labels := completionLabels(Analyze("Earthfile", source).Completions(offset, nil))
	require.ElementsMatch(t, []string{"COPY", "COMMAND"}, labels)
}

func TestCompletionsMatchCommandPrefixCaseInsensitively(t *testing.T) {
	t.Parallel()

	source, offset := cursorSource(t, "VERSION 0.8\n\nbuild:\n\tru|\n")

	require.Equal(t, []string{"RUN"}, completionLabels(Analyze("Earthfile", source).Completions(offset, nil)))
}

func TestCompletionsOfferLocalTargets(t *testing.T) {
	t.Parallel()

	source, offset := cursorSource(t,
		"VERSION 0.8\n\n# deps fetches modules.\ndeps:\n\tRUN true\n\ntest:\n\tRUN true\n\nall:\n\tBUILD +|\n")

	items := Analyze("Earthfile", source).Completions(offset, nil)
	require.ElementsMatch(t, []string{"deps", "test"}, completionLabels(items))

	for _, item := range items {
		if item.Label == "deps" {
			require.Equal(t, ItemTarget, item.Kind)
			require.Equal(t, "target +deps", item.Detail)
			require.Equal(t, "deps fetches modules.", item.Docs)
		}
	}
}

func TestCompletionsExcludeTheEnclosingTarget(t *testing.T) {
	t.Parallel()

	// A target that builds itself is always a cycle, so it is never a useful
	// candidate in its own recipe.
	source, offset := cursorSource(t, "VERSION 0.8\n\ndeps:\n\tRUN true\n\nall:\n\tBUILD +|\n")

	labels := completionLabels(Analyze("Earthfile", source).Completions(offset, nil))
	require.Equal(t, []string{"deps"}, labels)
	require.NotContains(t, labels, "all")
}

func TestCompletionsRestrictCandidatesByCommand(t *testing.T) {
	t.Parallel()

	doTarget, doOffset := cursorSource(t, "VERSION 0.8\n\nDEPLOY:\n\tFUNCTION\n\ndeps:\n\tRUN true\n\nall:\n\tDO +|\n")
	require.Equal(t, []string{"DEPLOY"},
		completionLabels(Analyze("Earthfile", doTarget).Completions(doOffset, nil)),
		"DO takes a function")

	buildTarget, buildOffset := cursorSource(t,
		"VERSION 0.8\n\nDEPLOY:\n\tFUNCTION\n\ndeps:\n\tRUN true\n\nall:\n\tBUILD +|\n")
	require.Equal(t, []string{"deps"},
		completionLabels(Analyze("Earthfile", buildTarget).Completions(buildOffset, nil)),
		"BUILD takes a target")
}

func TestCompletionsAcrossLocalImport(t *testing.T) {
	t.Parallel()

	rootPath := filepath.Clean("/workspace/Earthfile")
	libPath := filepath.Clean("/workspace/lib/Earthfile")
	libText := "VERSION 0.8\n# compile produces the binary.\ncompile:\n\tRUN true\n"

	rootText, offset := cursorSource(t, "VERSION 0.8\nIMPORT ./lib AS shared\n\napp:\n\tBUILD shared+|\n")
	loader := mapLoader{rootPath: rootText, libPath: libText}

	items := Analyze(rootPath, rootText).Completions(offset, loader)
	require.Equal(t, []string{"compile"}, completionLabels(items))
	require.Equal(t, "compile produces the binary.", items[0].Docs)
}

func TestCompletionsAcrossInlineLocalPath(t *testing.T) {
	t.Parallel()

	rootPath := filepath.Clean("/workspace/Earthfile")
	libPath := filepath.Clean("/workspace/lib/build.earth")
	libText := "VERSION 0.8\ncompile:\n\tRUN true\n"

	rootText, offset := cursorSource(t, "VERSION 0.8\n\napp:\n\tBUILD ./lib+|\n")
	loader := mapLoader{rootPath: rootText, libPath: libText}

	require.Equal(t, []string{"compile"},
		completionLabels(Analyze(rootPath, rootText).Completions(offset, loader)))
}

func TestCompletionsForUnresolvableProjectAreEmpty(t *testing.T) {
	t.Parallel()

	// A remote import cannot be read from the workspace, so completion stays
	// silent rather than failing the request.
	rootText, offset := cursorSource(t,
		"VERSION 0.8\nIMPORT github.com/example/lib AS remote\n\napp:\n\tBUILD remote+|\n")

	require.Empty(t, Analyze("Earthfile", rootText).Completions(offset, mapLoader{}))
}

func TestCompletionsAreEmptyWithoutAContext(t *testing.T) {
	t.Parallel()

	source, offset := cursorSource(t, "VERSION 0.8\n\nbuild:\n\t# a comment |\n")

	require.Empty(t, Analyze("Earthfile", source).Completions(offset, nil))
}

func TestCompletionsCarryTheReplaceRange(t *testing.T) {
	t.Parallel()

	source, offset := cursorSource(t, "VERSION 0.8\n\ndeps:\n\tRUN true\n\nall:\n\tBUILD +de|\n")

	items := Analyze("Earthfile", source).Completions(offset, nil)
	require.Len(t, items, 1)
	require.Equal(t, "de", source[items[0].Replace.Start:items[0].Replace.End])
}
