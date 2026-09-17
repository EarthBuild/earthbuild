package subcmd

import (
	"bytes"
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
	"github.com/EarthBuild/earthbuild/util/flagutil"
	"github.com/EarthBuild/earthbuild/util/hint"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/util/stringutil"
	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
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
	forceColor  bool
}

// writer returns the output sink, defaulting to [os.Stdout] for the zero value.
func (a *Doc) writer() io.Writer {
	if a.out == nil {
		return os.Stdout
	}

	return a.out
}

var (
	colorTitle  = makeDocColor(color.Bold)
	colorTarget = makeDocColor(color.Bold, color.FgCyan)
	colorArg    = makeDocColor(color.FgCyan)
	colorBadge  = makeDocColor(color.FgYellow)
)

func makeDocColor(attrs ...color.Attribute) *color.Color {
	c := color.New(attrs...)
	c.EnableColor()

	return c
}

func shouldColor(w io.Writer) bool {
	if color.NoColor {
		return false
	}

	if f, ok := w.(*os.File); ok {
		return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
	}

	return false
}

func (a *Doc) shouldColor() bool {
	if a.forceColor {
		return true
	}

	return shouldColor(a.writer())
}

type docStyler struct {
	enabled bool
}

func (s docStyler) style(c *color.Color, text string) string {
	if !s.enabled {
		return text
	}

	return c.Sprint(text)
}

func (s docStyler) badge(isRequired, isGlobal bool) (string, int) {
	switch {
	case isRequired && isGlobal:
		plain := " (required, global)"
		if !s.enabled {
			return plain, len(plain)
		}

		return " (" + s.style(colorBadge, "required") + ", " + s.style(colorBadge, "global") + ")", len(plain)
	case isRequired:
		plain := " (required)"
		if !s.enabled {
			return plain, len(plain)
		}

		return " " + s.style(colorBadge, "(required)"), len(plain)
	case isGlobal:
		plain := " (global)"
		if !s.enabled {
			return plain, len(plain)
		}

		return " " + s.style(colorBadge, "(global)"), len(plain)
	default:
		return "", 0
	}
}

func (a *Doc) styler() docStyler {
	return docStyler{enabled: a.shouldColor()}
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

	gitLookup := buildcontext.NewGitLookup(a.cli.Log(), a.cli.Flags().SSHAuthSock)
	resolver := buildcontext.NewResolver(nil, gitLookup, a.cli.Log(), "", a.cli.Flags().GitBranchOverride, "", 0, "")
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

		return a.documentSingleTarget(a.writer(), "", bc.Features, bc.Earthfile.BaseRecipe, tgt, true)
	}

	tgts := make([]earthfile.Target, 0, len(bc.Earthfile.Targets)+1)
	if len(bc.Earthfile.BaseRecipe) > 0 {
		tgts = append(tgts, makeBaseTarget(bc.Earthfile))
	}

	tgts = append(tgts, bc.Earthfile.Targets...)

	w := a.writer()
	fmt.Fprintln(w, a.styler().style(colorTitle, "TARGETS:"))

	const tgtIndent = docsIndent

	var documentedCount int

	for _, tgt := range tgts {
		var buf bytes.Buffer

		// Targets without a doc comment are silently skipped; any other error
		// (e.g. a malformed recipe body) is a real failure and propagates.
		err := a.documentSingleTarget(&buf, tgtIndent, bc.Features, bc.Earthfile.BaseRecipe, tgt, a.docShowLong)
		if err != nil {
			if errors.Is(err, errNoDocComment) {
				continue
			}

			return err
		}

		if documentedCount > 0 {
			fmt.Fprintln(w)
		}

		documentedCount++

		_, err = io.Copy(w, &buf)
		if err != nil {
			return err
		}
	}

	return nil
}

// parseDocTarget interprets the doc command's optional path argument. An empty
// path (or no argument) documents every target in the local "+base" Earthfile;
// a path containing '+' documents that single target. Remote paths are rejected.
func parseDocTarget(tgtPath string) (domain.Target, bool, error) {
	if tgtPath != "" {
		switch tgtPath[0] {
		case '.', '/', '+':
		default:
			return domain.Target{}, false, errors.New(
				"remote-paths are not currently supported - documentation targets must start with one of ['.', '/', '+']",
			)
		}
	}

	singleTgt := true

	if !strings.ContainsRune(tgtPath, '+') {
		tgtPath += docBaseTarget
		singleTgt = false
	}

	target, err := domain.ParseTarget(tgtPath)
	if err != nil {
		return domain.Target{}, false, fmt.Errorf("unable to parse target %q", tgtPath)
	}

	return target, singleTgt, nil
}

func docString(body string, names ...string) (string, error) {
	trimmed := strings.TrimLeft(body, " \t\r\n")
	if trimmed == "" {
		return "", errNoDocComment
	}

	firstWord := trimmed
	if idx := strings.IndexAny(trimmed, " \t\r\n"); idx != -1 {
		firstWord = trimmed[:idx]
	}

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

func printDocSections(w io.Writer, currIndent, scopeIndent, title string, st docStyler, sections ...docSection) {
	if len(sections) == 0 {
		return
	}

	fmt.Fprintln(w, indent(currIndent, st.style(colorTitle, title+":")))

	currIndent += scopeIndent

	for _, section := range sections {
		fmt.Fprintln(w, indent(currIndent, section.identifier))

		if section.body != "" {
			indented := indent(currIndent+scopeIndent, section.body)

			fmt.Fprintln(w, strings.Trim(indented, "\n"))
		}
	}
}

type targetDoc struct {
	args           []earthfile2llb.ArgInfo
	artifacts      []docSection
	localArtifacts []docSection
	images         []docSection
}

func (d *targetDoc) printArgsTable(w io.Writer, currIndent string, st docStyler) {
	if len(d.args) == 0 {
		return
	}

	colArg := len("ARG")
	colDflt := len("DEFAULT")

	for _, arg := range d.args {
		_, badgeLen := st.badge(arg.Required, arg.Global)
		if argLen := len("--"+arg.Name) + badgeLen; argLen > colArg {
			colArg = argLen
		}

		if arg.DefaultVal != nil {
			if dfltLen := len(unquote(*arg.DefaultVal)); dfltLen > colDflt {
				colDflt = dfltLen
			}
		}
	}

	const gap = "  "

	descIndent := currIndent + strings.Repeat(" ", colArg+len(gap)+colDflt+len(gap))

	// Header
	fmt.Fprint(w,
		currIndent,
		st.style(colorTitle, "ARG"),
		strings.Repeat(" ", colArg-len("ARG")),
		gap,
		st.style(colorTitle, "DEFAULT"),
		strings.Repeat(" ", colDflt-len("DEFAULT")),
		gap,
		st.style(colorTitle, "DESCRIPTION"),
		"\n",
	)

	// Rows
	for _, arg := range d.args {
		badge, badgeLen := st.badge(arg.Required, arg.Global)
		argText := st.style(colorArg, "--"+arg.Name) + badge
		argLen := len("--"+arg.Name) + badgeLen

		var dfltText string
		if arg.DefaultVal != nil {
			dfltText = unquote(*arg.DefaultVal)
		}

		fmt.Fprint(w, currIndent, argText)

		descLines := strings.Split(strings.TrimRight(arg.Description, "\r\n"), "\n")

		var firstLine string
		if len(descLines) > 0 {
			firstLine = strings.TrimSpace(descLines[0])
		}

		if dfltText != "" || firstLine != "" {
			fmt.Fprint(w, strings.Repeat(" ", colArg-argLen), gap, dfltText)

			if firstLine != "" {
				fmt.Fprint(w, strings.Repeat(" ", colDflt-len(dfltText)), gap, firstLine)
			}
		}

		fmt.Fprint(w, "\n")

		for _, line := range descLines[1:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				fmt.Fprint(w, "\n")

				continue
			}

			fmt.Fprint(w, descIndent, trimmed, "\n")
		}
	}
}

func (d *targetDoc) hasSections() bool {
	return len(d.artifacts) > 0 || len(d.localArtifacts) > 0 || len(d.images) > 0
}

func (d *targetDoc) printSections(w io.Writer, currIndent, scopeIndent string, st docStyler) {
	printDocSections(w, currIndent, scopeIndent, "ARTIFACTS", st, d.artifacts...)
	printDocSections(w, currIndent, scopeIndent, "LOCAL ARTIFACTS", st, d.localArtifacts...)
	printDocSections(w, currIndent, scopeIndent, "IMAGES", st, d.images...)
}

func (d *targetDoc) addArg(arg earthfile2llb.ArgInfo, docs string) {
	if arg.Description == "" {
		arg.Description, _ = docString(docs, arg.Name)
	}

	for i, existing := range d.args {
		if existing.Name == arg.Name {
			d.args[i] = arg

			return
		}
	}

	d.args = append(d.args, arg)
}

func referencedArgs(b earthfile.Block) map[string]bool {
	refs := make(map[string]bool)
	walkBlock(b, refs)

	return refs
}

func walkBlock(b earthfile.Block, refs map[string]bool) {
	for _, stmt := range b {
		if stmt.Command != nil {
			inspectCommand(stmt.Command, refs)
		}

		if stmt.With != nil {
			inspectCommand(&stmt.With.Command, refs)
			walkBlock(stmt.With.Body, refs)
		}

		if stmt.If != nil {
			for _, expr := range stmt.If.Expression {
				scanVarRefs(expr, refs)
			}

			walkBlock(stmt.If.IfBody, refs)

			for _, elif := range stmt.If.ElseIf {
				for _, expr := range elif.Expression {
					scanVarRefs(expr, refs)
				}

				walkBlock(elif.Body, refs)
			}

			if stmt.If.ElseBody != nil {
				walkBlock(*stmt.If.ElseBody, refs)
			}
		}

		if stmt.Try != nil {
			walkBlock(stmt.Try.TryBody, refs)

			if stmt.Try.CatchBody != nil {
				walkBlock(*stmt.Try.CatchBody, refs)
			}

			if stmt.Try.FinallyBody != nil {
				walkBlock(*stmt.Try.FinallyBody, refs)
			}
		}

		if stmt.For != nil {
			for _, arg := range stmt.For.Args {
				scanVarRefs(arg, refs)
			}

			walkBlock(stmt.For.Body, refs)
		}

		if stmt.Wait != nil {
			for _, arg := range stmt.Wait.Args {
				scanVarRefs(arg, refs)
			}

			walkBlock(stmt.Wait.Body, refs)
		}
	}
}

func inspectCommand(cmd *earthfile.Command, refs map[string]bool) {
	isInvocation := cmd.Name == earthfile.CmdBuild ||
		cmd.Name == earthfile.CmdDo ||
		cmd.Name == earthfile.CmdFrom

	for _, arg := range cmd.Args {
		scanVarRefs(arg, refs)

		if isInvocation {
			scanFlagRef(arg, refs)
		}
	}

	if cmd.Name == earthfile.CmdCopy {
		for _, arg := range stringutil.ProcessParamsAndQuotes(cmd.Args) {
			if flagutil.IsInParamsForm(arg) {
				_, params, err := flagutil.ParseParams(arg)
				if err == nil {
					for _, param := range params {
						scanFlagRef(param, refs)
					}
				}
			}
		}
	}
}

func scanFlagRef(s string, refs map[string]bool) {
	if !strings.HasPrefix(s, "--") {
		return
	}

	name := s[2:]
	if eq := strings.IndexByte(name, '='); eq != -1 {
		name = name[:eq]
	}

	if isIdent(name) {
		refs[name] = true
	}
}

func scanVarRefs(s string, refs map[string]bool) {
	i := 0
	for i < len(s) {
		if s[i] != '$' {
			i++
			continue
		}

		i++
		if i >= len(s) {
			break
		}

		if s[i] == '{' {
			i++

			start := i
			for i < len(s) && isIdentChar(s[i]) {
				i++
			}

			if i > start {
				refs[s[start:i]] = true
			}

			continue
		}

		start := i
		for i < len(s) && isIdentChar(s[i]) {
			i++
		}

		if i > start {
			refs[s[start:i]] = true
		}
	}
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}

	for i := range len(s) {
		if !isIdentChar(s[i]) {
			return false
		}
	}

	return true
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func parseDocSections(ft *features.Features, baseRcp, cmds earthfile.Block, isBase bool) (*targetDoc, error) {
	var d targetDoc

	if !isBase {
		refs := referencedArgs(cmds)

		for _, base := range baseRcp {
			if base.Command == nil || base.Command.Name != earthfile.CmdArg {
				continue
			}

			arg, err := earthfile2llb.ParseArg(*base.Command, true, ft.ExplicitGlobal)
			if err != nil {
				return nil, fmt.Errorf("failed to parse global ARG in base recipe: %w", err)
			}

			if arg.Global && refs[arg.Name] {
				d.addArg(arg, base.Command.Docs)
			}
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
			arg, err := earthfile2llb.ParseArg(cmd, isBase, ft.ExplicitGlobal)
			if err != nil {
				return nil, fmt.Errorf("failed to parse ARG: %w", err)
			}

			d.addArg(arg, cmd.Docs)
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
				d.localArtifacts = append(d.localArtifacts, artDoc)

				continue
			}

			d.artifacts = append(d.artifacts, artDoc)
		case earthfile.CmdSaveImage:
			identifiers, err := earthfile2llb.ImageNames(cmd)
			if err != nil {
				return nil, fmt.Errorf("could not parse SAVE IMAGE name(s): %w", err)
			}

			if len(identifiers) == 0 {
				continue
			}

			docs, _ := docString(cmd.Docs, identifiers...)
			d.images = append(d.images, docSection{
				identifier: strings.Join(identifiers, ", "),
				body:       docs,
			})
		}
	}

	return &d, nil
}

func (a *Doc) documentSingleTarget(
	w io.Writer,
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

	td, err := parseDocSections(ft, baseRcp, tgt.Recipe, tgt.Name == earthfile.TargetBase)
	if err != nil {
		return fmt.Errorf("failed to parse body of recipe '%v': %w", tgt.Name, err)
	}

	st := a.styler()

	const scopeIndent = "  "

	usage := indent(currIndent, st.style(colorTarget, "+"+tgt.Name))

	fmt.Fprintln(w, usage)

	docIndent := currIndent + scopeIndent + scopeIndent
	indented := indent(docIndent, docs)
	fmt.Fprintln(w, strings.Trim(indented, "\n"))

	if len(td.args) > 0 {
		fmt.Fprintln(w)
		td.printArgsTable(w, currIndent+scopeIndent, st)
	}

	if !includeBlockDocs {
		return nil
	}

	if td.hasSections() {
		fmt.Fprintln(w)
		td.printSections(w, currIndent+scopeIndent, scopeIndent, st)
	}

	return nil
}

func indent(prefix, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}

		lines[i] = prefix + l
	}

	return strings.Join(lines, "\n")
}

func findTarget(ef earthfile.Tree, name string) (earthfile.Target, error) {
	if name == earthfile.TargetBase {
		tgt := makeBaseTarget(ef)
		if tgt.Docs == "" {
			tgt.Docs = "base is the base target.\n"
		}

		return tgt, nil
	}

	for _, tgt := range ef.Targets {
		if tgt.Name == name {
			return tgt, nil
		}
	}

	return earthfile.Target{}, fmt.Errorf("could not find target named %q", name)
}

func makeBaseTarget(ef earthfile.Tree) earthfile.Target {
	return earthfile.Target{
		Name:   earthfile.TargetBase,
		Recipe: ef.BaseRecipe,
		Docs:   baseDocs(ef),
	}
}

func baseDocs(ef earthfile.Tree) string {
	if len(ef.BaseRecipe) > 0 {
		stmt := ef.BaseRecipe[0]
		if stmt.Command != nil && stmt.Command.Docs != "" {
			docs, err := docString(stmt.Command.Docs, earthfile.TargetBase)
			if err == nil {
				return docs
			}
		}
	}

	return ""
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[0] == s[len(s)-1] {
		return s[1 : len(s)-1]
	}

	return s
}
