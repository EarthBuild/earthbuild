package docker2earth

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/pkg/errors"
)

type lineWriter struct {
	buf         *bytes.Buffer
	currentLine int
	lineMap     []int
}

func (lw *lineWriter) WriteLine(content string, dockerfileLine int) {
	lw.buf.WriteString(content)
	lw.buf.WriteByte('\n')
	lw.currentLine++
	for len(lw.lineMap) <= lw.currentLine {
		lw.lineMap = append(lw.lineMap, 0)
	}
	lw.lineMap[lw.currentLine] = dockerfileLine
}

// GenerateNativeEarthfileAST parses a Dockerfile and converts it to a native earthfile.Tree AST.
// It maps the SourceLocation of each resulting statement back to the original Dockerfile.
func GenerateNativeEarthfileAST(dockerfilePath, imageTag string) (earthfile.Tree, error) {
	var in io.Reader
	in2, err := os.Open(dockerfilePath) // #nosec G304
	if err != nil {
		return earthfile.Tree{}, errors.Wrapf(err, "failed to open %q", dockerfilePath)
	}
	defer in2.Close()
	in = in2

	dockerfile, err := parser.Parse(in)
	if err != nil {
		return earthfile.Tree{}, errors.Wrapf(err, "failed to parse Dockerfile located at %q", dockerfilePath)
	}

	stages, initialArgs, err := instructions.Parse(dockerfile.AST)
	if err != nil {
		return earthfile.Tree{}, errors.Wrapf(err, "failed to parse Dockerfile located at %q", dockerfilePath)
	}

	absDockerfilePath, err := filepath.Abs(dockerfilePath)
	if err != nil {
		absDockerfilePath = dockerfilePath
	}

	lw := &lineWriter{
		buf:         &bytes.Buffer{},
		currentLine: 0,
		lineMap:     []int{0},
	}

	// Header
	lw.WriteLine(fmt.Sprintf("VERSION %s", earthCurrentVersion), 1)

	names := map[string]int{}

	for i, stage := range stages {
		stageLine := 1
		if len(stage.Location) > 0 {
			stageLine = stage.Location[0].Start.Line
		}

		lw.WriteLine(fmt.Sprintf("subbuild%d:", i+1), stageLine)

		// Initial ARGs
		if i == 0 && len(initialArgs) > 0 {
			for _, arg := range initialArgs {
				argLine := 1
				if len(arg.Location()) > 0 {
					argLine = arg.Location()[0].Start.Line
				}
				lw.WriteLine("    "+arg.String(), argLine)
			}
		}

		// FROM command
		fromLine := fmt.Sprintf("    FROM %s", stage.BaseName)
		if stage.Platform != "" {
			fromLine = fmt.Sprintf("    FROM --platform=%s %s", stage.Platform, stage.BaseName)
		}
		lw.WriteLine(fromLine, stageLine)

		if stage.Name == "" {
			names[strconv.Itoa(i)] = i
		} else {
			names[stage.Name] = i
		}

		for _, cmd := range stage.Commands {
			cmdLine := 1
			if len(cmd.Location()) > 0 {
				cmdLine = cmd.Location()[0].Start.Line
			}

			l := fmt.Sprintf("%v", cmd)
			if strings.HasPrefix(l, "COPY ") && strings.Contains(l, "--from") {
				parts := strings.Split(l, " ")
				if len(parts) == 4 {
					kv := strings.Split(parts[1], "=")
					if len(kv) == 2 {
						fromStageName := kv[1]
						n, ok := names[fromStageName]
						if ok {
							artifactName := getArtifactName(parts[2])
							lw.WriteLine(fmt.Sprintf("    COPY +subbuild%d/%s %s", n+1, artifactName, parts[3]), cmdLine)
							// Insert SAVE ARTIFACT in the source stage
							// Note: to simplify in-memory representation, we just append it in the generated string.
							// The Earthfile parser handles this.
							// Wait, we need to append SAVE ARTIFACT line-mapped to the current COPY line so it's clean.
							// Since it's in the target target, let's map it to the stage's target recipe.
							// But wait, the SAVE ARTIFACT must be executed in target n+1 (which is target i).
							// Since we are iterating, target n+1 was generated previously, so we cannot easily write to its recipe
							// here if it's already written.
							// Wait, the original convert.go writes targets by appending to a targets slice targets[n+1].
							// Let's replicate that logic to ensure COPY --from works!
						}
					}
				}
			} else if strings.HasPrefix(l, "ADD ") {
				return earthfile.Tree{}, errors.Errorf("earth does not support ADD, please convert to COPY instead")
			} else {
				lw.WriteLine("    "+l, cmdLine)
			}
		}
	}

	// We need to re-implement the targets slice generation so that we can support SAVE ARTIFACT appending dynamically!
	// Let's rewrite the target generation block to match Docker2Earth exactly but with line mapping.
	return generateASTFromStages(stages, initialArgs, names, absDockerfilePath, imageTag)
}

func generateASTFromStages(
	stages []instructions.Stage,
	initialArgs []instructions.ArgCommand,
	names map[string]int,
	absDockerfilePath string,
	imageTag string,
) (earthfile.Tree, error) {
	// Let's build a targets structure: targetIndex -> slice of lines (content, line number)
	type targetLine struct {
		content string
		line    int
	}

	targets := make([][]targetLine, len(stages)+1)

	// Target 0 is the version header
	targets[0] = []targetLine{
		{content: fmt.Sprintf("VERSION %s", earthCurrentVersion), line: 1},
	}

	for i, stage := range stages {
		stageLine := 1
		if len(stage.Location) > 0 {
			stageLine = stage.Location[0].Start.Line
		}

		targetIdx := i + 1
		targets[targetIdx] = append(targets[targetIdx], targetLine{
			content: fmt.Sprintf("subbuild%d:", targetIdx),
			line:    stageLine,
		})

		// Initial ARGs
		if i == 0 && len(initialArgs) > 0 {
			for _, arg := range initialArgs {
				argLine := 1
				if len(arg.Location()) > 0 {
					argLine = arg.Location()[0].Start.Line
				}
				targets[targetIdx] = append(targets[targetIdx], targetLine{
					content: "    " + arg.String(),
					line:    argLine,
				})
			}
		}

		// FROM command
		fromLine := fmt.Sprintf("    FROM %s", stage.BaseName)
		if stage.Platform != "" {
			fromLine = fmt.Sprintf("    FROM --platform=%s %s", stage.Platform, stage.BaseName)
		}
		targets[targetIdx] = append(targets[targetIdx], targetLine{
			content: fromLine,
			line:    stageLine,
		})

		if stage.Name == "" {
			names[strconv.Itoa(i)] = i
		} else {
			names[stage.Name] = i
		}

		for _, cmd := range stage.Commands {
			cmdLine := 1
			if len(cmd.Location()) > 0 {
				cmdLine = cmd.Location()[0].Start.Line
			}

			l := fmt.Sprintf("%v", cmd)
			if strings.HasPrefix(l, "COPY ") && strings.Contains(l, "--from") {
				parts := strings.Split(l, " ")
				if len(parts) == 4 {
					kv := strings.Split(parts[1], "=")
					if len(kv) == 2 {
						fromStageName := kv[1]
						n, ok := names[fromStageName]
						if ok {
							artifactName := getArtifactName(parts[2])
							targets[targetIdx] = append(targets[targetIdx], targetLine{
								content: fmt.Sprintf("    COPY +subbuild%d/%s %s", n+1, artifactName, parts[3]),
								line:    cmdLine,
							})
							targets[n+1] = append(targets[n+1], targetLine{
								content: fmt.Sprintf("    SAVE ARTIFACT %s %s", parts[2], artifactName),
								line:    cmdLine,
							})
						} else {
							// fallback
							targets[targetIdx] = append(targets[targetIdx], targetLine{
								content: "    " + l,
								line:    cmdLine,
							})
						}
					} else {
						targets[targetIdx] = append(targets[targetIdx], targetLine{
							content: "    " + l,
							line:    cmdLine,
						})
					}
				} else {
					targets[targetIdx] = append(targets[targetIdx], targetLine{
						content: "    " + l,
						line:    cmdLine,
					})
				}
			} else if strings.HasPrefix(l, "ADD ") {
				return earthfile.Tree{}, errors.Errorf("earth does not support ADD, please convert to COPY instead")
			} else {
				targets[targetIdx] = append(targets[targetIdx], targetLine{
					content: "    " + l,
					line:    cmdLine,
				})
			}
		}
	}

	// Add final SAVE IMAGE
	lastIdx := len(stages)
	targets[lastIdx] = append(targets[lastIdx], targetLine{
		content: "    SAVE IMAGE " + imageTag,
		line:    1,
	})

	// Add final build target
	targets = append(targets, []targetLine{
		{content: "build:", line: 1},
		{content: fmt.Sprintf("    BUILD +subbuild%d", len(stages)), line: 1},
	})

	// Write everything to a buffer and populate the lineMap
	buf := &bytes.Buffer{}
	currentLine := 0
	lineMap := []int{0}

	for i, targetLines := range targets {
		for j, tl := range targetLines {
			if i == 0 {
				buf.WriteString(tl.content)
				buf.WriteByte('\n')
				currentLine++
				lineMap = append(lineMap, tl.line)
			} else {
				if j == 0 {
					buf.WriteString(tl.content)
					buf.WriteByte('\n')
					currentLine++
					lineMap = append(lineMap, tl.line)
				} else {
					buf.WriteString(tl.content)
					buf.WriteByte('\n')
					currentLine++
					lineMap = append(lineMap, tl.line)
				}
			}
		}
	}

	// Parse the generated Earthfile string
	ef, err := earthfile.Parse(absDockerfilePath, buf.String(), earthfile.WithSourceMap())
	if err != nil {
		return earthfile.Tree{}, errors.Wrap(err, "failed to parse in-memory generated Earthfile")
	}

	if ef.SourceLocation == nil {
		ef.SourceLocation = &earthfile.SourceLocation{
			File: absDockerfilePath,
		}
	}

	// Remap SourceLocations back to original Dockerfile lines
	remapSourceLocations(&ef, lineMap, absDockerfilePath)

	return ef, nil
}

func remapSourceLocations(ef *earthfile.Tree, lineMap []int, dockerfilePath string) {
	remap := func(sl *earthfile.SourceLocation) {
		if sl == nil {
			return
		}
		sl.File = dockerfilePath
		if sl.StartLine > 0 && sl.StartLine < len(lineMap) {
			sl.StartLine = lineMap[sl.StartLine]
		}
		if sl.EndLine > 0 && sl.EndLine < len(lineMap) {
			sl.EndLine = lineMap[sl.EndLine]
		}
	}

	remap(ef.SourceLocation)
	if ef.Version != nil {
		remap(ef.Version.SourceLocation)
	}

	var walkBlock func(block earthfile.Block)
	walkBlock = func(block earthfile.Block) {
		for i := range block {
			stmt := &block[i]
			remap(stmt.SourceLocation)
			if stmt.Command != nil {
				remap(stmt.Command.SourceLocation)
			}
			if stmt.With != nil {
				remap(stmt.With.SourceLocation)
				remap(stmt.With.Command.SourceLocation)
				walkBlock(stmt.With.Body)
			}
			if stmt.If != nil {
				remap(stmt.If.SourceLocation)
				walkBlock(stmt.If.IfBody)
				if stmt.If.ElseBody != nil {
					walkBlock(*stmt.If.ElseBody)
				}
				for j := range stmt.If.ElseIf {
					remap(stmt.If.ElseIf[j].SourceLocation)
					walkBlock(stmt.If.ElseIf[j].Body)
				}
			}
			if stmt.Try != nil {
				remap(stmt.Try.SourceLocation)
				walkBlock(stmt.Try.TryBody)
				if stmt.Try.CatchBody != nil {
					walkBlock(*stmt.Try.CatchBody)
				}
				if stmt.Try.FinallyBody != nil {
					walkBlock(*stmt.Try.FinallyBody)
				}
			}
			if stmt.For != nil {
				remap(stmt.For.SourceLocation)
				walkBlock(stmt.For.Body)
			}
			if stmt.Wait != nil {
				remap(stmt.Wait.SourceLocation)
				walkBlock(stmt.Wait.Body)
			}
		}
	}

	for i := range ef.Targets {
		remap(ef.Targets[i].SourceLocation)
		walkBlock(ef.Targets[i].Recipe)
	}

	for i := range ef.Functions {
		remap(ef.Functions[i].SourceLocation)
		walkBlock(ef.Functions[i].Recipe)
	}

	walkBlock(ef.BaseRecipe)
}
