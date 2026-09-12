package cli

import (
	"fmt"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/config"
)

// ConfigCmd groups configuration bootstrap commands. Init seeds the user
// configuration file from the embedded defaults (refusing to overwrite an
// existing file unless forced), validate checks one file without writing,
// and print reports the effective normalized configuration.
type ConfigCmd struct {
	Init     ConfigInitCmd     `cmd:"" help:"Seed the user configuration file."`
	Validate ConfigValidateCmd `cmd:"" help:"Validate a configuration file."`
	Print    ConfigPrintCmd    `cmd:"" help:"Print the effective configuration."`
}

// ConfigInitCmd is `pan config init`.
type ConfigInitCmd struct {
	Path  string `name:"path" type:"path" help:"Target path; defaults to the user configuration path."`
	Force bool   `name:"force" help:"Overwrite an existing file; without this flag an existing file is refused and reported as skipped."`
}

// Run executes `pan config init`.
func (c ConfigInitCmd) Run(kctx *kong.Context, root *Root, deps Deps) error {
	path := c.Path
	if path == "" {
		userPath, err := config.UserPath()
		if err != nil {
			return err
		}
		path = userPath
	}
	outcome, err := config.InitAt(path, c.Force)
	if err != nil {
		return err
	}
	var messages []string
	if outcome.Action == "skipped" {
		// A skipped init is reported as an informational diagnostic so
		// automation can detect the refusal without parsing prose.
		messages = append(messages, fmt.Sprintf("%s already exists; re-run with --force to overwrite", outcome.Path))
	}
	return emitDiagnostics(kctx, root, deps, outcome, messages)
}

// ConfigValidateCmd is `pan config validate`.
type ConfigValidateCmd struct {
	Path string `name:"path" type:"path" help:"File to validate; defaults to the user configuration path."`
}

// Run executes `pan config validate`.
func (c ConfigValidateCmd) Run(kctx *kong.Context, root *Root, deps Deps) error {
	path := c.Path
	if path == "" {
		userPath, err := config.UserPath()
		if err != nil {
			return err
		}
		path = userPath
	}
	check, err := config.CheckFile(path)
	if err != nil {
		return err
	}
	var messages []string
	if !check.Exists {
		messages = append(messages, fmt.Sprintf("%s does not exist", check.Path))
	} else if !check.Valid {
		messages = append(messages, check.Error)
	}
	if err := emitDiagnostics(kctx, root, deps, check, messages); err != nil {
		return err
	}
	if len(messages) > 0 {
		return &ExitError{Code: ExitFailure, Err: fmt.Errorf("configuration file %s is not valid", path)}
	}
	return nil
}

// ConfigPrintCmd is `pan config print`.
type ConfigPrintCmd struct{}

// Run executes `pan config print`.
func (ConfigPrintCmd) Run(kctx *kong.Context, root *Root, deps Deps) error {
	return emitResult(kctx, root, deps, displayRepo(root.Repo), deps.App.EffectiveConfig())
}
