// Package dockerfile converts Dockerfile build instructions into Earthfile ASTs.
package dockerfile

import (
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

func cmdSourceLocation(cmd instructions.Command, filePath string) *earthfile.SourceLocation {
	locs := cmd.Location()
	if len(locs) == 0 {
		if filePath == "" {
			return nil
		}

		return &earthfile.SourceLocation{File: filePath}
	}

	r := locs[0]

	return &earthfile.SourceLocation{
		File:        filePath,
		StartLine:   r.Start.Line,
		StartColumn: r.Start.Character,
		EndLine:     r.End.Line,
		EndColumn:   r.End.Character,
	}
}

// DefaultTargetName is the default Earthfile target name for Dockerfile conversion.
const DefaultTargetName = "build"

func stageTargetName(stage instructions.Stage, index, totalStages int) string {
	if stage.Name != "" {
		return strings.ToLower(stage.Name)
	}

	if totalStages == 1 || index == totalStages-1 {
		return DefaultTargetName
	}

	return fmt.Sprintf("stage-%d", index+1)
}

// convert converts parsed Dockerfile stages into an Earthfile AST.
func convert(
	stages []instructions.Stage,
	args []instructions.ArgCommand,
	commandName string,
	imageTag string,
	filePath string,
) (*earthfile.Tree, []string, error) {
	names := make(map[string]int, len(stages))
	targetNames := make([]string, len(stages))
	recipes := make([]earthfile.Block, len(stages))
	targets := make([]earthfile.Target, 0, len(stages))

	for i, stage := range stages {
		targetNames[i] = stageTargetName(stage, i, len(stages))

		names[strconv.Itoa(i)] = i

		if stage.Name != "" {
			names[stage.Name] = i
			names[strings.ToLower(stage.Name)] = i
		}
	}

	for i, stage := range stages {
		if i == 0 {
			for _, arg := range args {
				parts := strings.SplitN(arg.String(), " ", 2)

				var cmdArgs []string

				if len(parts) > 1 {
					cmdArgs = strings.Fields(parts[1])
				}

				recipes[i] = append(recipes[i], earthfile.Statement{
					Command: &earthfile.Command{
						Name: earthfile.CmdArg,
						Args: cmdArgs,
					},
				})
			}
		}

		recipes[i] = append(recipes[i], earthfile.Statement{
			Command: &earthfile.Command{
				Name: earthfile.CmdFrom,
				Args: []string{stage.BaseName},
			},
		})

		for _, cmd := range stage.Commands {
			var (
				cmdName earthfile.Cmd
				cmdArgs []string
			)

			stmtLoc := cmdSourceLocation(cmd, filePath)

			switch c := cmd.(type) {
			case *instructions.AddCommand:
				return nil, nil, errors.New("earth does not support ADD, please convert to COPY instead")
			case *instructions.CopyCommand:
				if c.From != "" {
					fromStageName := c.From

					n, ok := names[fromStageName]
					if !ok {
						return nil, nil, fmt.Errorf("unknown stage %q in --from", fromStageName)
					}

					srcPath := c.SourcePaths[0]
					artName := path.Base(srcPath)

					recipes[n] = append(recipes[n], earthfile.Statement{
						Command: &earthfile.Command{
							Name: earthfile.CmdSaveArtifact,
							Args: []string{srcPath, artName},
						},
					})

					cmdName = earthfile.CmdCopy
					cmdArgs = []string{"+" + targetNames[n] + "/" + artName, c.DestPath}
				} else {
					cmdName = earthfile.Cmd(strings.ToUpper(c.Name()))
					cmdArgs = append(slices.Clone(c.SourcePaths), c.DestPath)
				}
			default:
				cmdName = earthfile.Cmd(strings.ToUpper(cmd.Name()))

				cmdStr := fmt.Sprintf("%v", cmd)
				if strings.HasPrefix(strings.ToUpper(cmdStr), string(cmdName)+" ") {
					if argsStr := strings.TrimSpace(cmdStr[len(cmdName)+1:]); argsStr != "" {
						cmdArgs = []string{argsStr}
					}
				}
			}

			recipes[i] = append(recipes[i], earthfile.Statement{
				SourceLocation: stmtLoc,
				Command: &earthfile.Command{
					SourceLocation: stmtLoc,
					Name:           cmdName,
					Args:           cmdArgs,
				},
			})
		}

		if i == len(stages)-1 && imageTag != "" {
			recipes[i] = append(recipes[i], earthfile.Statement{
				Command: &earthfile.Command{
					Name: earthfile.CmdSaveImage,
					Args: []string{imageTag},
				},
			})
		}
	}

	hasBuildTarget := slices.Contains(targetNames, DefaultTargetName)

	for i, targetName := range targetNames {
		var docs string
		if targetName == DefaultTargetName && (len(targetNames) == 1 || i == len(targetNames)-1) {
			docs = fmt.Sprintf("# %s builds the final image", DefaultTargetName)
		}

		targets = append(targets, earthfile.Target{
			Docs:   docs,
			Name:   targetName,
			Recipe: recipes[i],
		})
	}

	if !hasBuildTarget && len(targetNames) > 0 {
		finalTarget := targetNames[len(targetNames)-1]
		targets = append(targets, earthfile.Target{
			Docs: "# build is the default entrypoint",
			Name: DefaultTargetName,
			Recipe: earthfile.Block{
				{
					Command: &earthfile.Command{
						Name: earthfile.CmdBuild,
						Args: []string{"+" + finalTarget},
					},
				},
			},
		})
	}

	headerComment := "# This Earthfile was converted from a Dockerfile.\n"
	if commandName != "" {
		headerComment = fmt.Sprintf("# This Earthfile was converted from a Dockerfile using '%s'.\n", commandName)
	}

	baseRecipe := earthfile.Block{
		{
			Command: &earthfile.Command{
				Docs: headerComment +
					"# Conversion is done on a best-effort basis and might not follow all\n" +
					"# best practices. Please visit https://docs.earthbuild.dev for guides.",
			},
		},
	}

	return &earthfile.Tree{
		Version: &earthfile.Version{
			Args: []string{earthfile.CurrentVersion},
		},
		BaseRecipe: baseRecipe,
		Targets:    targets,
	}, targetNames, nil
}

// Convert parses Dockerfile content from r and writes the converted Earthfile AST to w.
// It returns the target names generated for each stage.
func Convert(w io.Writer, r io.Reader, commandName string, imageTag string, filePath string) ([]string, error) {
	dockerfile, err := parser.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parse Dockerfile: %w", err)
	}

	stages, initialArgs, err := instructions.Parse(dockerfile.AST)
	if err != nil {
		return nil, fmt.Errorf("parse Dockerfile instructions: %w", err)
	}

	tree, targetNames, err := convert(stages, initialArgs, commandName, imageTag, filePath)
	if err != nil {
		return nil, err
	}

	err = tree.Format(w)
	if err != nil {
		return nil, fmt.Errorf("format Earthfile: %w", err)
	}

	return targetNames, nil
}
