package cli

import (
	"context"

	"github.com/alecthomas/kong"
	"github.com/dotcommander/pan/internal/pipeline/initialize"
)

// FlowInitCmd is `pan flow init`.
type FlowInitCmd struct {
	Force bool `name:"force" help:"Overwrite an existing pan.yaml."`
}

// Run creates a starter pipeline spec in the selected repository.
func (c FlowInitCmd) Run(kctx *kong.Context, root *Root, deps Deps, _ context.Context) error {
	result, err := initialize.Init(root.Repo, c.Force)
	if err != nil {
		return err
	}
	return emitResult(kctx, root, deps, root.Repo, struct {
		Output  string `json:"output"`
		Project string `json:"project"`
	}{result.Output, result.Project})
}
