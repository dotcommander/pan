package clean

import (
	"path/filepath"
	"sort"
)

func newPlan() Plan {
	return Plan{
		Schema:                PlanSchema,
		DeleteCandidates:      []Candidate{},
		DevArtifactCandidates: []Candidate{},
		ArchiveCandidates:     []Candidate{},
		BrokenLinks:           []Candidate{},
		LargeFiles:            []Candidate{},
		MisplacedScripts:      []Candidate{},
		MisplacedDocs:         []Candidate{},
		UntrackCandidates:     []Candidate{},
		RenameDocs:            []Candidate{},
		AllFiles:              []LabeledFile{},
		Summary:               map[string]int{},
	}
}

// add records one claimed candidate and its disposition label.
func (c *categorizer) add(bucket *[]Candidate, cand Candidate, status, reason string) {
	*bucket = append(*bucket, cand)
	c.plan.AllFiles = append(c.plan.AllFiles, LabeledFile{File: cand.File, Status: status, Reason: reason, SizeKB: cand.SizeKB})
}

// labelClean records a file as clean with its reason.
func (c *categorizer) labelClean(f *FileInfo, reason string) {
	c.plan.AllFiles = append(c.plan.AllFiles, LabeledFile{File: filepath.ToSlash(f.RelPath), Status: StatusClean, Reason: reason, SizeKB: f.Size / 1024})
}

// Categorize applies the priority-ordered rule chain to every walked file
// and produces the cleanup plan.
func Categorize(files []FileInfo, opts Options) Plan {
	var scriptNames []string
	for i := range files {
		if isMisplacedScript(&files[i]) {
			scriptNames = append(scriptNames, filepath.Base(files[i].RelPath))
		}
	}
	referenced := referencedScripts(opts.Root, scriptNames)
	protected := protectedLiveBinaries(opts.HomeDir)
	c := newCategorizer(opts, referenced, protected)
	for i := range files {
		c.categorize(&files[i])
	}
	summarize(&c.plan)
	return c.plan.Sorted()
}

// categorize runs the rule chain for one file; an unclaimed regular file
// is labeled clean.
func (c *categorizer) categorize(f *FileInfo) {
	rel := filepath.ToSlash(f.RelPath)
	name := filepath.Base(rel)
	ext := filepath.Ext(name)
	for _, rule := range c.ruleChain {
		if rule(f, rel, name, ext) {
			return
		}
	}
	c.labelClean(f, "")
}

// summarize fills the bucket counts, the total, and the health score.
func summarize(plan *Plan) {
	plan.Summary[CatDelete] = len(plan.DeleteCandidates)
	plan.Summary[CatDevArtifact] = len(plan.DevArtifactCandidates)
	plan.Summary[CatArchive] = len(plan.ArchiveCandidates)
	plan.Summary[CatBrokenLink] = len(plan.BrokenLinks)
	plan.Summary[CatLargeFile] = len(plan.LargeFiles)
	plan.Summary[CatMisplaced] = len(plan.MisplacedScripts)
	plan.Summary[CatMisplacedDoc] = len(plan.MisplacedDocs)
	plan.Summary[CatUntrack] = len(plan.UntrackCandidates)
	plan.Summary[CatRenameDocs] = len(plan.RenameDocs)
	plan.Summary["total"] = len(plan.DeleteCandidates) + len(plan.DevArtifactCandidates) +
		len(plan.ArchiveCandidates) + len(plan.BrokenLinks) + len(plan.LargeFiles) +
		len(plan.MisplacedScripts) + len(plan.MisplacedDocs) + len(plan.UntrackCandidates) +
		len(plan.RenameDocs)
	plan.HealthScore = CalculateHealth(*plan)
}

func boolPtr(b bool) *bool { return &b }

// Sorted returns the plan with every bucket and the disposition inventory
// ordered by file path, so identical trees always produce identical plans.
func (p Plan) Sorted() Plan {
	buckets := []*[]Candidate{
		&p.DeleteCandidates, &p.DevArtifactCandidates, &p.ArchiveCandidates,
		&p.BrokenLinks, &p.LargeFiles, &p.MisplacedScripts, &p.MisplacedDocs,
		&p.UntrackCandidates, &p.RenameDocs,
	}
	for _, bucket := range buckets {
		sort.Slice(*bucket, func(i, j int) bool { return (*bucket)[i].File < (*bucket)[j].File })
	}
	sort.Slice(p.AllFiles, func(i, j int) bool { return p.AllFiles[i].File < p.AllFiles[j].File })
	return p
}
