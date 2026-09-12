package spec

// Summary holds display-ready metadata for a served pipeline page.
type Summary struct {
	Title      string
	Breadcrumb string
	Basename   string
	Collection string
	SourcePath string
	PhaseCount int
	StoreCount int
}

// URL returns the unambiguous route for this spec.
func (s Summary) URL() string {
	if s.Collection == "" {
		return "/" + s.Basename
	}
	return "/" + s.Collection + "/" + s.Basename
}

// AsSummary builds display metadata for a parsed pipeline spec.
func (s *Spec) AsSummary(sourcePath, collection, basename string) Summary {
	return Summary{
		Title:      s.Title,
		Breadcrumb: s.Breadcrumb,
		Basename:   basename,
		Collection: collection,
		SourcePath: sourcePath,
		PhaseCount: len(s.Phases),
		StoreCount: len(s.Stores),
	}
}

// CollectionSummary describes a named collection of served specs.
type CollectionSummary struct {
	Name       string
	SourcePath string
	Specs      []Summary
}

// Single reports whether this collection came from one explicitly supplied file.
func (c CollectionSummary) Single() bool {
	return len(c.Specs) == 1 && c.Specs[0].Basename == c.Name
}

// URL returns the collection landing route.
func (c CollectionSummary) URL() string { return "/" + c.Name }
