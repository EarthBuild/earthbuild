package subcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/buildcontext"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/util/hint"
	"github.com/EarthBuild/earthbuild/util/platutil"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/urfave/cli/v3"
)

// docBaseTarget is the implicit target documented when no '+target' is given.
const docBaseTarget = "+base"

// errNoDocComment is the sentinel returned when a target has no usable doc
// comment. When documenting all targets these are skipped, so the call site
// distinguishes them (via [errors.Is]) from real parse/resolve failures.
var errNoDocComment = errors.New("no doc comment found")

// Doc encapsulates the doc command logic.
type Doc struct {
	cli CLI

	// out is where rendered docs are written; nil means [os.Stdout]. Injectable
	// so tests can capture output without hijacking the global stdout.
	out io.Writer

	docShowLong bool
}

// writer returns the output sink, defaulting to [os.Stdout] for the zero value.
func (a *Doc) writer() io.Writer {
	if a.out == nil {
		return os.Stdout
	}

	return a.out
}

// NewDoc creates a new Doc command.
func NewDoc(cli CLI) *Doc {
	return &Doc{
		cli: cli,
	}
}

// Cmds returns the list of commands for the doc command.
func (a *Doc) Cmds() []*cli.Command {
	return []*cli.Command{
		{
			Name:        "doc",
			Usage:       "Document targets from an Earthfile",
			UsageText:   "earth [options] doc [<earthfile-ref>[+<target-ref>]]",
			Description: "Document targets from an Earthfile by reading in line comments.",
			Action:      a.action,
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:        "long",
					Aliases:     []string{"l"},
					Usage:       "Show full details for all target inputs and outputs",
					Destination: &a.docShowLong,
				},
			},
		},
	}
}

func (a *Doc) action(ctx context.Context, cmd *cli.Command) error {
	a.cli.SetCommandName("docTarget")

	if cmd.NArg() > 1 {
		return errors.New("invalid number of arguments provided")
	}

	var tgtPath string
	if cmd.NArg() > 0 {
		tgtPath = cmd.Args().Get(0)
	}

	target, singleTgt, err := parseDocTarget(tgtPath)
	if err != nil {
		return err
	}

	gitLookup := buildcontext.NewGitLookup(a.cli.Console(), a.cli.Flags().SSHAuthSock)
	resolver := buildcontext.NewResolver(nil, gitLookup, a.cli.Console(), "", a.cli.Flags().GitBranchOverride, "", 0, "")
	platr := platutil.NewResolver(platutil.GetUserPlatform())

	var gwClient gwclient.Client

	bc, err := resolver.Resolve(ctx, gwClient, platr, target)
	if err != nil {
		return fmt.Errorf("failed to resolve target: %w", err)
	}

	const docsIndent = "  "

	if singleTgt {
		tgt, err := findTarget(bc.Earthfile, target.Target)
		if err != nil {
			return fmt.Errorf("failed to look up target: %w", err)
		}

		return a.documentSingleTarget("", bc.Features, bc.Earthfile.BaseRecipe, tgt, true)
	}

	tgts := bc.Earthfile.Targets

	fmt.Fprintln(a.writer(), "TARGETS:")

	const tgtIndent = docsIndent
	for _, tgt := range tgts {
		// Targets without a doc comment are silently skipped; any other error
		// (e.g. a malformed recipe body) is a real failure and propagates.
		err := a.documentSingleTarget(tgtIndent, bc.Features, bc.Earthfile.BaseRecipe, tgt, a.docShowLong)
		if err != nil && !errors.Is(err, errNoDocComment) {
			return err
		}
	}

	return nil
}

// parseDocTarget interprets the doc command's optional path argument. An empty
// path (or no argument) documents every target in the local "+base" Earthfile;
// a path containing '+' documents that single target. Remote paths are rejected.
func parseDocTarget(tgtPath string) (target domain.Target, singleTgt bool, err error) {
	if tgtPath != "" {
		switch tgtPath[0] {
		case '.', '/', '+':
		default:
			return domain.Target{}, false, errors.New(
				"remote-paths are not currently supported - documentation targets must start with one of ['.', '/', '+']",
			)
		}
	}

	singleTgt = true

	if !strings.ContainsRune(tgtPath, '+') {
		tgtPath += docBaseTarget
		singleTgt = false
	}

	target, err = domain.ParseTarget(tgtPath)
	if err != nil {
		return domain.Target{}, false, fmt.Errorf("unable to parse target %q", tgtPath)
	}

	return target, singleTgt, nil
}

func docString(body string, names ...string) (string, error) {
	firstWordEnd := strings.IndexRune(body, ' ')
	if firstWordEnd == -1 {
		return "", errors.New("failed to parse first word of documentation comments")
	}

	firstWord := body[:firstWordEnd]
	if slices.Contains(names, firstWord) {
		return body, nil
	}

	return "", hint.Wrapf(errNoDocComment,
		"a comment was found but the first word was not one of (%s)", strings.Join(names, ", "))
}

type docSection struct {
	identifier string
	body       string
}

func docSectionsOutput(currIndent, scopeIndent, title string, sections ...docSection) string {
	if len(sections) == 0 {
		return ""
	}

	var out strings.Builder

	out.WriteString(indent(currIndent, title+":"))
	out.WriteByte('\n')

	currIndent += scopeIndent
	for _, section := range sections {
		out.WriteString(indent(currIndent, section.identifier))
		out.WriteByte('\n')

		if section.body == "" {
			continue
		}

		indented := indent(currIndent+scopeIndent, section.body)
		out.WriteString(strings.Trim(indented, "\n"))
		out.WriteByte('\n')
	}

	return out.String()
}

type blockIO struct {
	requiredArgs   []docSection
	optionalArgs   []docSection
	artifacts      []docSection
	localArtifacts []docSection
	images         []docSection
}

func (b blockIO) options() string {
	var sb strings.Builder

	for _, arg := range b.requiredArgs {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}

		sb.WriteString(arg.identifier)
	}

	for _, arg := range b.optionalArgs {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}

		sb.WriteByte('[')
		sb.WriteString(arg.identifier)
		sb.WriteByte(']')
	}

	return sb.String()
}

func (b blockIO) help(indent, scopeIndent string) string {
	return docSectionsOutput(indent, scopeIndent, "REQUIRED ARGS", b.requiredArgs...) +
		docSectionsOutput(indent, scopeIndent, "OPTIONAL ARGS", b.optionalArgs...) +
		docSectionsOutput(indent, scopeIndent, "ARTIFACTS", b.artifacts...) +
		docSectionsOutput(indent, scopeIndent, "LOCAL ARTIFACTS", b.localArtifacts...) +
		docSectionsOutput(indent, scopeIndent, "IMAGES", b.images...)
}

func addArg(b *blockIO, ft *features.Features, stmt earthfile.Statement, isBase, onlyGlobal bool) error {
	if stmt.Command == nil {
		return nil
	}

	cmd := *stmt.Command
	if cmd.Name != earthfile.CmdArg {
		return nil
	}

	ident, dflt, isRequired, isGlobal, err := earthfile2llb.ArgName(cmd, isBase, ft.ExplicitGlobal)
	if err != nil {
		return fmt.Errorf("failed to parse ARG statement: %w", err)
	}

	if onlyGlobal && !isGlobal {
		return nil
	}

	docs, _ := docString(cmd.Docs, ident)

	doc := docSection{
		identifier: "--" + ident,
		body:       docs,
	}
	if dflt != nil {
		doc.identifier += "=" + *dflt
	}

	if isRequired {
		b.requiredArgs = append(b.requiredArgs, doc)
		return nil
	}

	b.optionalArgs = append(b.optionalArgs, doc)

	return nil
}

func parseDocSections(ft *features.Features, baseRcp, cmds earthfile.Block) (*blockIO, error) {
	var b blockIO
	for _, base := range baseRcp {
		err := addArg(&b, ft, base, true, true)
		if err != nil {
			return nil, fmt.Errorf("failed to parse global ARG in base recipe: %w", err)
		}
	}

	for _, rb := range cmds {
		if rb.Command == nil {
			continue
		}

		cmd := *rb.Command
		//nolint:exhaustive // Only doc-extractable commands (ARG, SAVE ARTIFACT, SAVE IMAGE) are processed here.
		switch cmd.Name {
		case earthfile.CmdArg:
			err := addArg(&b, ft, rb, false, false)
			if err != nil {
				return nil, fmt.Errorf("failed to parse non-global ARG: %w", err)
			}
		case earthfile.CmdSaveArtifact:
			name, localName, err := earthfile2llb.ArtifactName(cmd)
			if err != nil {
				return nil, fmt.Errorf("could not parse SAVE ARTIFACT name: %w", err)
			}

			idents := []string{name}
			if localName != nil {
				idents = append(idents, *localName)
			}

			docs, _ := docString(cmd.Docs, idents...)

			artDoc := docSection{
				identifier: name,
				body:       docs,
			}
			if localName != nil {
				artDoc.identifier += " -> " + *localName
				b.localArtifacts = append(b.localArtifacts, artDoc)

				continue
			}

			b.artifacts = append(b.artifacts, artDoc)
		case earthfile.CmdSaveImage:
			identifiers, err := earthfile2llb.ImageNames(cmd)
			if err != nil {
				return nil, fmt.Errorf("could not parse SAVE IMAGE name(s): %w", err)
			}

			if len(identifiers) == 0 {
				continue
			}

			docs, _ := docString(cmd.Docs, identifiers...)
			b.images = append(b.images, docSection{
				identifier: strings.Join(identifiers, ", "),
				body:       docs,
			})
		}
	}

	return &b, nil
}

func (a *Doc) documentSingleTarget(
	currIndent string,
	ft *features.Features,
	baseRcp earthfile.Block,
	tgt earthfile.Target,
	includeBlockDocs bool,
) error {
	if tgt.Docs == "" {
		return hint.Wrapf(errNoDocComment,
			"add a comment starting with the word '%s' on the line immediately above this target", tgt.Name)
	}

	docs, err := docString(tgt.Docs, tgt.Name)
	if err != nil {
		return err
	}

	blockIO, err := parseDocSections(ft, baseRcp, tgt.Recipe)
	if err != nil {
		return fmt.Errorf("failed to parse body of recipe '%v': %w", tgt.Name, err)
	}

	const scopeIndent = "  "

	usage := indent(currIndent, "+"+tgt.Name)

	options := blockIO.options()
	if options != "" {
		usage += " " + options
	}

	w := a.writer()
	fmt.Fprintln(w, usage)

	docIndent := currIndent + scopeIndent + scopeIndent
	indented := indent(docIndent, docs)
	fmt.Fprintln(w, strings.Trim(indented, "\n"))

	if !includeBlockDocs {
		return nil
	}

	fmt.Fprintln(w, blockIO.help(currIndent+scopeIndent, scopeIndent))

	return nil
}

func indent(indent, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}

		lines[i] = indent + l
	}

	return strings.Join(lines, "\n")
}

func findTarget(ef earthfile.Tree, name string) (earthfile.Target, error) {
	for _, tgt := range ef.Targets {
		if tgt.Name == name {
			return tgt, nil
		}
	}

	return earthfile.Target{}, fmt.Errorf("could not find target named %q", name)
}
