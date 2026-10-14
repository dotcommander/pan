package clean

import (
	"path/filepath"
	"strings"
)

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

// ruleProtected preserves configured file names and live system binaries.
func (c *categorizer) ruleProtected(f *FileInfo, _, name, _ string) bool {
	if !f.IsDir && c.protectedNames[name] {
		c.labelClean(f, "protected file name")
		return true
	}
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
