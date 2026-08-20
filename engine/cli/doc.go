package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// Doc prints the documentation an Earthfile carries.
//
// Not a build, like List: the file is read and nothing is planned or run. What
// it prints is what a `#` comment above a target says about it (E474).
//
// **A comment is documentation when it begins with the name of what it
// documents.** That rule is the parser's rather than this command's -
// `tests/target-docs.earth` has a target whose comment does not, and the AST
// hands it over with no docs at all - so this is formatting and not judgement.
//
// `--long` adds what the recipe declares: the arguments a caller must supply,
// the ones it may, and what the target produces. Two of those section headers
// are pinned by the corpus and the rest of the shape is this engine's.
func Doc(o Options) error {
	tree, err := readTree(o.Dir)
	if err != nil {
		return err
	}

	out := io.Writer(io.Discard)
	if o.Out != nil {
		out = o.Out
	}

	var b strings.Builder

	b.WriteString("TARGETS:\n")

	for _, t := range tree.Targets {
		if t.Docs == "" {
			continue
		}

		fmt.Fprintf(&b, "  +%s\n", t.Name)
		writeDocs(&b, t.Docs)

		if o.Long {
			writeRecipe(&b, t.Recipe)
		}
	}

	_, err = io.WriteString(out, b.String())

	return err
}

// writeDocs indents documentation under the target it belongs to.
//
// A blank line stays blank. Indenting it would be invisible in a terminal and
// would break the corpus's multiline match, which is a diff nobody can see -
// so the emptiness is checked here rather than trusted to a trailing-space lint.
func writeDocs(b *strings.Builder, docs string) {
	for _, line := range strings.Split(strings.TrimRight(docs, "\n"), "\n") {
		if line == "" {
			b.WriteString("\n")

			continue
		}

		fmt.Fprintf(b, "      %s\n", line)
	}
}

// writeRecipe prints what a target's recipe declares and produces.
//
// Grouped by what the reader is asking. "What must I give this target" and "what
// will I get back" are different questions, and a single list ordered by where
// the commands happen to appear answers neither.
func writeRecipe(b *strings.Builder, recipe earthfile.Block) {
	var required, optional, artifacts, images []string

	for _, s := range recipe {
		c := s.Command
		if c == nil {
			continue
		}

		switch c.Name {
		case earthfile.CmdArg:
			name, isRequired := argNameAndKind(c.Args)
			if name == "" {
				continue
			}

			entry := described(name, c.Docs)
			if isRequired {
				required = append(required, entry)
			} else {
				optional = append(optional, entry)
			}

		case earthfile.CmdSaveArtifact:
			if name := artifactName(c.Args); name != "" {
				artifacts = append(artifacts, described(name, c.Docs))
			}

		case earthfile.CmdSaveImage:
			images = append(images, imageNames(c.Args, c.Docs)...)
		}
	}

	for _, section := range []struct {
		title string
		items []string
	}{
		{"REQUIRED ARGS", required},
		{"ARGS", optional},
		{"ARTIFACTS", artifacts},
		{"IMAGES", images},
	} {
		if len(section.items) == 0 {
			continue
		}

		fmt.Fprintf(b, "\n    %s:\n", section.title)

		for _, item := range section.items {
			fmt.Fprintf(b, "      %s\n", item)
		}
	}

	b.WriteString("\n")
}

// described puts a name and its one-line documentation together.
//
// **A comment documents what it names.** The parser applies that rule to a
// target's own comment and hands over the rest as written, so it is applied here
// for the things a recipe declares: `tests/doc-recipe-block.earth` calls three
// of its own comments undocumented - an argument, an artifact and an image - and
// each of them sits directly above the thing it is not documentation for.
//
// Without the rule every comment in a recipe reads as documentation, and a
// reader asking what an argument is for is answered about something else (E474).
func described(name, docs string) string {
	docs = strings.TrimSpace(strings.ReplaceAll(docs, "\n", " "))
	if docs == "" || !documents(docs, name) {
		return name
	}

	return name + " - " + docs
}

// documents reports whether a comment begins with the name of its subject.
//
// The first *word*, so that `bar.txt is a documented artifact` documents
// `bar.txt` and `barn.txt is ...` does not - a prefix match would take the
// second for the first.
func documents(docs, name string) bool {
	first, _, _ := strings.Cut(docs, " ")

	return strings.EqualFold(strings.TrimRight(first, ":,"), name)
}

// argNameAndKind reads an ARG's name, and whether a caller has to supply it.
//
// `--required` is the only flag that changes the answer: a name with a default
// is one the caller *may* set, and one without is only required when it says so.
func argNameAndKind(args []string) (string, bool) {
	required := slices.Contains(args, "--required")

	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			continue
		}

		name, _, _ := strings.Cut(a, "=")

		return strings.TrimSpace(name), required
	}

	return "", required
}

// artifactName reads what a SAVE ARTIFACT is called from the reader's side.
//
// The local destination when there is one, because that is the name the reader
// will see on disk; otherwise the path inside the image.
func artifactName(args []string) string {
	for i, a := range args {
		if strings.EqualFold(a, "AS") && i+2 < len(args) &&
			strings.EqualFold(args[i+1], "LOCAL") {
			return args[i+2]
		}
	}

	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			return a
		}
	}

	return ""
}

// imageNames reads the tags a SAVE IMAGE declares.
//
// A SAVE IMAGE may name several at once, and each is a thing the reader can ask
// for - `tests/doc-recipe-block.earth` says so about the second name of a pair.
// One with no name at all is a cache hint rather than an image, and there is
// nothing for a reader to ask for.
func imageNames(args []string, docs string) []string {
	var out []string

	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			continue
		}

		out = append(out, described(a, docs))
	}

	return out
}
