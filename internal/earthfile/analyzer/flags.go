package analyzer

import (
	"reflect"
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// Flag is a command option an editor can offer.
type Flag struct {
	// Name is the option spelling without its leading dashes.
	Name string
	// Description is the canonical help text for the option.
	Description string
	// Argument reports whether the option takes a value.
	Argument bool
}

// commandOptions maps each canonical command to the option struct that
// declares its flags. Reading the flags from those structs keeps completion in
// step with the builder, which parses the very same tags.
//
// TestEveryOptionStructIsMappedToACommand fails when cmdopts grows a struct
// that is not listed here.
var commandOptions = map[string]reflect.Type{
	string(earthfile.CmdArg):   reflect.TypeOf(cmdopts.Arg{}),
	string(earthfile.CmdBuild): reflect.TypeOf(cmdopts.Build{}),
	string(earthfile.CmdCache): reflect.TypeOf(cmdopts.Cache{}),
	string(earthfile.CmdCopy):  reflect.TypeOf(cmdopts.Copy{}),
	string(earthfile.CmdDo):    reflect.TypeOf(cmdopts.Do{}),
	// WITH DOCKER is a block prefix rather than a single canonical keyword, so
	// its options belong to DOCKER.
	string(earthfile.CmdDocker):         reflect.TypeOf(cmdopts.WithDocker{}),
	string(earthfile.CmdFor):            reflect.TypeOf(cmdopts.For{}),
	string(earthfile.CmdFrom):           reflect.TypeOf(cmdopts.From{}),
	string(earthfile.CmdFromDockerfile): reflect.TypeOf(cmdopts.FromDockerfile{}),
	string(earthfile.CmdGitClone):       reflect.TypeOf(cmdopts.GitClone{}),
	string(earthfile.CmdHealthCheck):    reflect.TypeOf(cmdopts.HealthCheck{}),
	string(earthfile.CmdIf):             reflect.TypeOf(cmdopts.If{}),
	string(earthfile.CmdImport):         reflect.TypeOf(cmdopts.Import{}),
	string(earthfile.CmdLet):            reflect.TypeOf(cmdopts.Let{}),
	string(earthfile.CmdProject):        reflect.TypeOf(cmdopts.Project{}),
	string(earthfile.CmdRun):            reflect.TypeOf(cmdopts.Run{}),
	string(earthfile.CmdSaveArtifact):   reflect.TypeOf(cmdopts.SaveArtifact{}),
	string(earthfile.CmdSaveImage):      reflect.TypeOf(cmdopts.SaveImage{}),
	string(earthfile.CmdSet):            reflect.TypeOf(cmdopts.Set{}),
}

// CommandFlags returns the options a command accepts, sorted by name. An
// unrecognized command, or one that takes no options, yields none.
func CommandFlags(command string) []Flag {
	options, ok := commandOptions[command]
	if !ok {
		return nil
	}

	var flags []Flag

	for i := range options.NumField() {
		field := options.Field(i)

		name, ok := field.Tag.Lookup("long")
		if !ok || name == "" {
			continue
		}

		flags = append(flags, Flag{
			Name:        name,
			Description: field.Tag.Get("description"),
			Argument:    field.Type.Kind() != reflect.Bool,
		})
	}

	slices.SortFunc(flags, func(a, b Flag) int {
		return strings.Compare(a.Name, b.Name)
	})

	return flags
}

func flagCompletions(ctx CompletionContext) []Completion {
	var items []Completion

	for _, flag := range CommandFlags(ctx.Command) {
		if !hasFoldedPrefix(flag.Name, ctx.Prefix) {
			continue
		}

		insert := "--" + flag.Name
		if flag.Argument {
			insert += "="
		}

		items = append(items, Completion{
			Label:   "--" + flag.Name,
			Insert:  insert,
			Detail:  ctx.Command + " option",
			Docs:    flag.Description,
			Kind:    ItemFlag,
			Replace: ctx.Replace,
		})
	}

	return items
}
