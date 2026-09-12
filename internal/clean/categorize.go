package clean

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/config"
)

// cleanRule attempts one categorization rule on one file; it reports
// whether the rule claimed the file and the chain should stop.
type cleanRule func(f *FileInfo, rel, name, ext string) bool

// categorizer applies the priority-ordered rule chain to walked files and
// accumulates the plan. Categorization is pure once the precomputed
// referenced-script and protected-binary maps are known; it never touches
// the filesystem.
type categorizer struct {
	rules         config.CleanRules
	allowedMD     map[string]bool
	scaffolds     map[string]bool
	deleteNames   map[string]bool
	deleteExts    map[string]bool
	deleteDirs    map[string]bool
	archiveExts   map[string]bool
	dataExts      map[string]bool
	untrackExts   map[string]bool
	imageExts     map[string]bool
	dotWhitelist  map[string]bool
	archivePrefix string
	ignoreName    string
	largeBytes    int64
	referenced    map[string]bool
	protected     map[string]bool
	plan          Plan
	ruleChain     []cleanRule
}

func newCategorizer(opts Options, referenced, protected map[string]bool) *categorizer {
	rules := opts.Rules
	c := &categorizer{
		rules:         rules,
		allowedMD:     toSet(rules.AllowedRootMD),
		scaffolds:     toSet(rules.ScaffoldFiles),
		deleteNames:   toSet(rules.DeleteNames),
		deleteExts:    toSet(rules.DeleteExtensions),
		deleteDirs:    toSet(rules.DeleteDirectories),
		archiveExts:   toSet(rules.ArchiveExtensions),
		dataExts:      toSet(rules.DataExtensions),
		untrackExts:   toSet(rules.UntrackExtensions),
		imageExts:     toSet(rules.ImageExtensions),
		dotWhitelist:  toSet(rules.DotfileAllowlist),
		archivePrefix: slashClean(rules.ArchiveDir),
		ignoreName:    slashClean(rules.IgnoreFile),
		largeBytes:    opts.largeFileBytes(),
		referenced:    referenced,
		protected:     protected,
	}
	c.plan = newPlan()
	c.ruleChain = []cleanRule{
		c.ruleWorkspace, c.ruleSuppressed, c.ruleBrokenLink, c.ruleGitUnknown, c.ruleProtected,
		c.ruleHidden, c.ruleIgnored, c.ruleMigration, c.ruleData, c.ruleScaffold,
		c.ruleDelete, c.ruleLarge, c.ruleDevArtifact,
		c.ruleMisplacedDoc, c.ruleDocRename, c.ruleMisplacedScript,
		c.ruleUntrack, c.ruleUntrackedFile, c.ruleDirectory,
	}
	return c
}

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

// ruleWorkspace leaves pan's own archive workspace and the ignore file
// untouched.
func (c *categorizer) ruleWorkspace(f *FileInfo, rel, _, _ string) bool {
	if rel != c.archivePrefix && !strings.HasPrefix(rel, c.archivePrefix+"/") && rel != c.ignoreName {
		return false
	}
	c.labelClean(f, "cleanup workspace")
	return true
}

func (c *categorizer) ruleSuppressed(f *FileInfo, _, _, _ string) bool {
	if !f.Suppressed {
		return false
	}
	c.labelClean(f, "suppressed")
	return true
}

func (c *categorizer) ruleGitUnknown(f *FileInfo, _, _, _ string) bool {
	if !f.GitStateUnknown {
		return false
	}
	c.labelClean(f, "repository state unknown")
	return true
}

// ruleProtected leaves live system binaries (<home>/go/bin symlinks into
// this tree) untouchable.
func (c *categorizer) ruleProtected(f *FileInfo, _, _, _ string) bool {
	return c.protected[f.Path]
}

// ruleHidden skips dotfiles and dot-directories unless allowlisted.
func (c *categorizer) ruleHidden(_ *FileInfo, rel, name, _ string) bool {
	hidden := strings.HasPrefix(name, ".") || strings.HasPrefix(rel, ".")
	return hidden && !c.dotWhitelist[name] && !strings.HasPrefix(name, ".env.")
}

// ruleIgnored applies the narrow, conservative ignored-file rules.
func (c *categorizer) ruleIgnored(f *FileInfo, rel, name, ext string) bool {
	if !f.Ignored {
		return false
	}
	c.addIgnored(f, rel, name, ext)
	return true
}

func (c *categorizer) addIgnored(f *FileInfo, rel, name, ext string) {
	cand, disposition := c.ignoredDisposition(f, rel, name, ext)
	switch disposition {
	case StatusDelete:
		c.add(&c.plan.DeleteCandidates, Candidate{File: cand.File, Reason: cand.Reason, SizeKB: f.Size / 1024, Score: Score(f)}, StatusDelete, cand.Reason)
	case StatusArchive:
		c.add(&c.plan.ArchiveCandidates, Candidate{File: cand.File, Reason: cand.Reason, SizeKB: f.Size / 1024, Score: Score(f)}, StatusArchive, cand.Reason)
	}
}

// ignoredDisposition applies the narrow ignored-file rules. The returned
// candidate's Reason carries the why; the disposition is StatusDelete,
// StatusArchive, or "" for "leave alone".
func (c *categorizer) ignoredDisposition(f *FileInfo, rel, name, ext string) (Candidate, string) {
	if f.IsDir {
		return Candidate{}, ""
	}
	if c.deleteNames[name] {
		return Candidate{File: rel, Reason: "ignored system file"}, StatusDelete
	}
	if cand, disposition, matched := c.ignoredSafeDir(rel, ext); matched {
		return cand, disposition
	}
	if strings.Contains(name, ".backup.") {
		return Candidate{File: rel, Reason: "ignored backup file"}, StatusDelete
	}
	for _, suf := range c.rules.IgnoredDevDocSuffixes {
		if strings.HasSuffix(name, suf) {
			return Candidate{File: rel, Reason: "ignored dev document"}, StatusDelete
		}
	}
	return c.ignoredRootDisposition(rel, name, ext)
}

// ignoredSafeDir decides the disposition for a file inside one configured
// safe directory: an extensionless binary under bin/ is stale build
// output; everything else in a safe directory is expected runtime data.
// matched reports whether any safe directory claimed the path.
func (c *categorizer) ignoredSafeDir(rel, ext string) (cand Candidate, disposition string, matched bool) {
	for _, sd := range c.rules.SafeDirectories {
		if !strings.HasPrefix(rel, sd) {
			continue
		}
		if strings.HasPrefix(rel, "bin/") && ext == "" {
			return Candidate{File: rel, Reason: "ignored stale binary"}, StatusDelete, true
		}
		return Candidate{}, "", true
	}
	return Candidate{}, "", false
}

// ignoredRootDisposition decides the disposition for an ignored file at
// the repository root.
func (c *categorizer) ignoredRootDisposition(rel, name, ext string) (Candidate, string) {
	if strings.Contains(rel, "/") {
		return Candidate{}, ""
	}
	for _, pfx := range c.rules.IgnoredDeletePrefixes {
		if strings.HasPrefix(name, pfx) {
			return Candidate{File: rel, Reason: "ignored dev script"}, StatusDelete
		}
	}
	if c.deleteExts[ext] {
		return Candidate{File: rel, Reason: "ignored temp file"}, StatusDelete
	}
	if c.archiveExts[ext] {
		return Candidate{File: rel, Reason: "archive file at repo root"}, StatusArchive
	}
	return Candidate{}, ""
}

// ruleMigration leaves migration files alone: they are source, not data
// artifacts.
func (c *categorizer) ruleMigration(f *FileInfo, rel, _, _ string) bool {
	if f.IsDir || (!strings.Contains(rel, "/migrations/") && !strings.HasPrefix(rel, "migrations/")) {
		return false
	}
	c.labelClean(f, "")
	return true
}

// ruleData always archives data files: they are never deleted.
func (c *categorizer) ruleData(f *FileInfo, rel, _, ext string) bool {
	if f.IsDir || !c.dataExts[ext] {
		return false
	}
	reason := "DATA FILE — may contain important data, review before removing"
	if f.Tracked {
		reason = "DATA FILE (tracked) — may contain important data"
	}
	c.add(&c.plan.ArchiveCandidates, Candidate{File: rel, Reason: reason, SizeKB: f.Size / 1024, Score: Score(f), ContentHint: "data"}, StatusArchive, reason)
	return true
}

// ruleScaffold removes tracked scaffold remnants from project init.
func (c *categorizer) ruleScaffold(f *FileInfo, rel, _, _ string) bool {
	if !f.Tracked || !c.scaffolds[rel] {
		return false
	}
	c.add(&c.plan.DeleteCandidates, Candidate{File: rel, Reason: "scaffold remnant", SizeKB: f.Size / 1024, Score: Score(f)}, StatusDelete, "scaffold remnant")
	return true
}

func (c *categorizer) ruleDelete(f *FileInfo, rel, _, _ string) bool {
	ok, reason := isDeleteCandidate(f, c.deleteNames, c.deleteExts, c.deleteDirs)
	if !ok {
		return false
	}
	c.add(&c.plan.DeleteCandidates, Candidate{File: rel, Reason: reason, SizeKB: f.Size / 1024, Score: Score(f)}, StatusDelete, reason)
	return true
}

func (c *categorizer) ruleBrokenLink(f *FileInfo, rel, _, _ string) bool {
	if !f.IsSymlink || f.LinkTarget == "" {
		return false
	}
	c.add(&c.plan.BrokenLinks, Candidate{File: rel, SizeKB: f.Size / 1024, Target: f.LinkTarget, Score: Score(f)}, StatusBrokenLink, "broken symlink → "+f.LinkTarget)
	return true
}

func (c *categorizer) ruleLarge(f *FileInfo, rel, _, _ string) bool {
	if c.largeBytes <= 0 || f.IsDir || f.Size <= c.largeBytes {
		return false
	}
	c.add(&c.plan.LargeFiles, Candidate{File: rel, SizeKB: f.Size / 1024, Tracked: boolPtr(f.Tracked), Score: Score(f)}, StatusLargeFile, "file over configured size bound")
	return true
}

// ruleDevArtifact archives tracked dev artifacts: loose scratch files near
// the root, not nested reference docs.
func (c *categorizer) ruleDevArtifact(f *FileInfo, rel, _, _ string) bool {
	if !isDevArtifact(f, c.rules) {
		return false
	}
	c.add(&c.plan.DevArtifactCandidates, Candidate{File: rel, Reason: "tracked dev artifact", SizeKB: f.Size / 1024, Score: Score(f)}, StatusDevArtifact, "tracked dev artifact")
	return true
}

func (c *categorizer) ruleMisplacedDoc(f *FileInfo, rel, _, _ string) bool {
	if !isMisplacedDoc(f, c.allowedMD) {
		return false
	}
	c.add(&c.plan.MisplacedDocs, Candidate{File: rel, Reason: "root .md → docs/", SizeKB: f.Size / 1024, Score: Score(f)}, StatusMisplacedDoc, "root .md → docs/")
	return true
}

func (c *categorizer) ruleDocRename(f *FileInfo, rel, _, _ string) bool {
	newPath, ok := needsDocRename(f)
	if !ok {
		return false
	}
	c.add(&c.plan.RenameDocs, Candidate{File: rel, Target: newPath, SizeKB: f.Size / 1024, Tracked: boolPtr(f.Tracked)}, StatusRenameDoc, "rename → "+newPath)
	return true
}

func (c *categorizer) ruleMisplacedScript(f *FileInfo, rel, name, _ string) bool {
	if !isMisplacedScript(f) {
		return false
	}
	ref := c.referenced[name]
	c.add(&c.plan.MisplacedScripts, Candidate{File: rel, SizeKB: f.Size / 1024, Tracked: boolPtr(f.Tracked), Referenced: boolPtr(ref), Score: Score(f)}, StatusMisplacedScript, "")
	return true
}

func (c *categorizer) ruleUntrack(f *FileInfo, rel, _, _ string) bool {
	ok, reason := isUntrackCandidate(f, untrackPolicy{exts: c.untrackExts, images: c.imageExts, dirs: c.rules.UntrackDirectories})
	if !ok {
		return false
	}
	c.add(&c.plan.UntrackCandidates, Candidate{File: rel, Reason: reason, SizeKB: f.Size / 1024, Score: Score(f)}, StatusUntrack, reason)
	return true
}

// ruleUntrackedFile archives untracked regular files with their duplicate
// and staleness context.
func (c *categorizer) ruleUntrackedFile(f *FileInfo, rel, _, _ string) bool {
	if f.Tracked || f.IsDir {
		return false
	}
	reason := "untracked file"
	if f.HasFinding(RuleDuplicate) {
		reason += ", potential duplicate of " + filepath.ToSlash(f.Duplicate)
	}
	cand := Candidate{File: rel, Reason: reason, SizeKB: f.Size / 1024, Score: Score(f)}
	if hint := f.Content.String(); hint != contentHintUnknown && hint != "meaningful" {
		cand.ContentHint = hint
	}
	if f.StaleDays > 0 {
		cand.StaleDays = f.StaleDays
	}
	c.add(&c.plan.ArchiveCandidates, cand, StatusArchive, reason)
	return true
}

// ruleDirectory silently leaves directories that matched no rule.
func (c *categorizer) ruleDirectory(f *FileInfo, _, _, _ string) bool {
	return f.IsDir
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

func isDeleteCandidate(f *FileInfo, deleteNames, deleteExts, deleteDirs map[string]bool) (bool, string) {
	name := filepath.Base(f.RelPath)
	if deleteNames[name] {
		return true, "system file"
	}
	ext := filepath.Ext(name)
	if deleteExts[ext] || strings.HasSuffix(name, "~") {
		return true, "temp/backup file"
	}
	if f.IsDir && deleteDirs[name] {
		return true, "cache directory"
	}
	if f.HasFinding(RuleEmpty) {
		return true, "empty directory"
	}
	return false, ""
}

func isDevArtifact(f *FileInfo, rules config.CleanRules) bool {
	if !f.Tracked || f.IsDir {
		return false
	}
	// Dev artifacts are loose scratch files near the root, not nested
	// reference docs.
	if strings.Count(filepath.ToSlash(f.RelPath), "/") > 1 {
		return false
	}
	name := filepath.Base(f.RelPath)
	for _, p := range rules.DevArtifactPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	for _, s := range rules.DevArtifactSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

func isMisplacedDoc(f *FileInfo, allowedMD map[string]bool) bool {
	if f.IsDir || !f.Tracked {
		return false
	}
	name := filepath.Base(f.RelPath)
	rel := filepath.ToSlash(f.RelPath)
	return strings.HasSuffix(name, ".md") && !strings.Contains(rel, "/") && !allowedMD[name]
}

// untrackPolicy bundles the untrack pattern tables so disposition
// checks stay within the argument limit.
type untrackPolicy struct {
	exts   map[string]bool
	images map[string]bool
	dirs   []string
}

// isUntrackCandidate reports whether a tracked file should leave the git
// index, with the reason.
func isUntrackCandidate(f *FileInfo, policy untrackPolicy) (bool, string) {
	if !f.Tracked || f.IsDir || f.IsSymlink {
		return false, ""
	}
	name := filepath.Base(f.RelPath)
	rel := filepath.ToSlash(f.RelPath)
	ext := filepath.Ext(name)
	return untrackDisposition(f, name, rel, ext, policy)
}

func untrackDisposition(f *FileInfo, name, rel, ext string, policy untrackPolicy) (bool, string) {
	if policy.exts[ext] {
		return true, "binary/archive extension"
	}
	if policy.images[ext] && !strings.Contains(rel, "/") {
		return true, "image file at repo root"
	}
	if isEnvFile(name) {
		return true, "environment file"
	}
	if underAnyPrefix(rel, policy.dirs) {
		return true, "build output directory"
	}
	if isCompiledBinary(f, rel, ext) {
		return true, "compiled binary"
	}
	if f.HasFinding(RuleGenerated) {
		return true, "generated content"
	}
	return false, ""
}

// isEnvFile matches .env and non-sample .env.* files.
func isEnvFile(name string) bool {
	if name == ".env" {
		return true
	}
	return strings.HasPrefix(name, ".env.") &&
		!strings.HasSuffix(name, ".example") && !strings.HasSuffix(name, ".sample") && !strings.HasSuffix(name, ".template")
}

func underAnyPrefix(rel string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// isCompiledBinary matches extensionless executables outside scripts/ and
// hooks/.
func isCompiledBinary(f *FileInfo, rel, ext string) bool {
	return f.Executable && ext == "" && !strings.HasPrefix(rel, "scripts/") && !strings.HasPrefix(rel, "hooks/")
}

// normalizeDocName returns the kebab-case lowercase form of a filename, or
// "" when the name is already normalized.
func normalizeDocName(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	normalized := strings.ToLower(strings.ReplaceAll(base, "_", "-")) + strings.ToLower(ext)
	if normalized == name {
		return ""
	}
	return normalized
}

func needsDocRename(f *FileInfo) (string, bool) {
	if f.IsDir {
		return "", false
	}
	rel := filepath.ToSlash(f.RelPath)
	if !strings.HasPrefix(rel, "docs/") {
		return "", false
	}
	name := filepath.Base(rel)
	if strings.EqualFold(name, "readme.md") || strings.HasSuffix(name, "_test.go") {
		return "", false
	}
	if norm := normalizeDocName(name); norm != "" {
		return path.Join(path.Dir(rel), norm), true
	}
	return "", false
}

func isMisplacedScript(f *FileInfo) bool {
	if f.IsDir || f.IsSymlink {
		return false
	}
	rel := filepath.ToSlash(f.RelPath)
	return strings.HasSuffix(rel, ".sh") && !strings.Contains(rel, "/")
}

// protectedLiveBinaries resolves symlinks under <home>/go/bin and returns
// the set of absolute target paths that are live system binaries. Any file
// at one of those paths must never be touched.
func protectedLiveBinaries(home string) map[string]bool {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil
		}
	}
	goBin := filepath.Join(home, "go", "bin")
	entries, err := os.ReadDir(goBin)
	if err != nil {
		return nil
	}
	targets := make(map[string]bool)
	for _, e := range entries {
		link := filepath.Join(goBin, e.Name())
		target, err := filepath.EvalSymlinks(link)
		if err != nil || target == link {
			continue
		}
		targets[target] = true
	}
	return targets
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
