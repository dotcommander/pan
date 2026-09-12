package cli

import (
	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/pipeline/seed"
	"github.com/dotcommander/pan/internal/pipeline/spec"
)

// FlowConfigCmd reports the pipeline directory or installs bundled examples.
type FlowConfigCmd struct {
	Setup     bool   `help:"Install bundled pipeline examples."`
	Force     bool   `help:"Replace existing bundled examples during setup."`
	Directory string `name:"directory" type:"path" help:"Pipeline directory; defaults to the user pipeline directory."`
}

// Run executes pipeline configuration without loading repository analysis.
func (c FlowConfigCmd) Run(kctx *kong.Context, root *Root, deps Deps) error {
	dir := c.Directory
	if dir == "" {
		var err error
		dir, err = spec.DataDir()
		if err != nil {
			return err
		}
	}
	var written []string
	if c.Setup {
		var err error
		written, err = seed.Seed(dir, c.Force)
		if err != nil {
			return err
		}
	}
	return emitResult(kctx, root, deps, displayRepo(root.Repo), struct {
		Directory string   `json:"directory"`
		Written   []string `json:"written,omitempty"`
	}{dir, written})
}
