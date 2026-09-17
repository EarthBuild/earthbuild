package earthfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// grammarKeywordPattern pulls the alternation out of the VS Code grammar's
// command rule.
var grammarKeywordPattern = regexp.MustCompile(`^\^\[ \\t\]\*\((.+)\)\\b$`)

type textMateGrammar struct {
	Repository map[string]struct {
		Match string `json:"match"`
	} `json:"repository"`
}

// TestTextMateKeywordParity keeps the VS Code grammar's keyword list in step
// with the canonical command set. VS Code has no Tree-sitter, so its shallow
// grammar restates the keywords; this is the gate that stops them drifting,
// as the Tree-sitter parity suite does for editors that do use a grammar.
func TestTextMateKeywordParity(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "editors", "vscode", "syntaxes", "earthfile.tmLanguage.json")

	contents, err := os.ReadFile(path) // #nosec G304 -- the grammar is repository-owned.
	require.NoError(t, err)

	var grammar textMateGrammar
	require.NoError(t, json.Unmarshal(contents, &grammar))

	rule, ok := grammar.Repository["command"]
	require.True(t, ok, "grammar must define a command rule")

	match := grammarKeywordPattern.FindStringSubmatch(rule.Match)
	require.NotNil(t, match, "command rule must be an anchored keyword alternation, got %q", rule.Match)

	inGrammar := make([]string, 0, len(Commands()))

	for _, alternative := range strings.Split(match[1], "|") {
		// The grammar spells the gap in a two-word command as a character
		// class so that extra spacing still highlights.
		inGrammar = append(inGrammar, strings.ReplaceAll(alternative, `[ \t]+`, " "))
	}

	expected := make([]string, 0, len(Commands()))
	for _, cmd := range Commands() {
		expected = append(expected, string(cmd))
	}

	slices.Sort(inGrammar)
	slices.Sort(expected)

	require.Equal(t, expected, inGrammar,
		"the VS Code grammar must list exactly the canonical commands")
}

// TestTextMateKeywordsAreOrderedLongestFirst guards the alternation order.
// TextMate picks the first matching alternative, so a bare SAVE listed before
// SAVE ARTIFACT would leave ARTIFACT unhighlighted.
func TestTextMateKeywordsAreOrderedLongestFirst(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "editors", "vscode", "syntaxes", "earthfile.tmLanguage.json")

	contents, err := os.ReadFile(path) // #nosec G304 -- the grammar is repository-owned.
	require.NoError(t, err)

	var grammar textMateGrammar
	require.NoError(t, json.Unmarshal(contents, &grammar))

	match := grammarKeywordPattern.FindStringSubmatch(grammar.Repository["command"].Match)
	require.NotNil(t, match)

	alternatives := strings.Split(match[1], "|")

	for i, alternative := range alternatives {
		spelled := strings.ReplaceAll(alternative, `[ \t]+`, " ")

		for _, earlier := range alternatives[:i] {
			earlierSpelled := strings.ReplaceAll(earlier, `[ \t]+`, " ")
			require.False(t, strings.HasPrefix(spelled, earlierSpelled+" "),
				"%q must be listed before %q", spelled, earlierSpelled)
		}
	}
}
