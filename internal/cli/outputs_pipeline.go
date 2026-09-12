package cli

// pipelineSurfaces describes reusable pipeline artifacts rather than their
// surrounding command-result envelopes.
func pipelineSurfaces() []OutputSurface {
	return []OutputSurface{
		{
			Name: "pipeline-yaml", Producer: "flow scan",
			MediaType: "application/yaml", BestFor: "reusable specs for flow render, validate, and review",
			Privacy:       "absolute repository root, source paths, symbols, and authored spec fields",
			Limits:        []string{"Go source scanning", "--output - streams YAML and rejects --format json", "existing output requires --force or a matching --update target"},
			Flags:         []string{flagRepo, flagOutput, "--update", "--force", "--max-phases", "--max-stages"},
			Compatibility: "Pan pipeline spec model; Pan owns default paths and file-result envelopes",
		},
	}
}
