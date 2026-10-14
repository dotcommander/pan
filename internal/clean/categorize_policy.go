package clean

import (
	"os"
	"path/filepath"

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
	rules          config.CleanRules
	allowedMD      map[string]bool
	scaffolds      map[string]bool
	deleteNames    map[string]bool
	deleteExts     map[string]bool
	deleteDirs     map[string]bool
	archiveExts    map[string]bool
	dataExts       map[string]bool
	untrackExts    map[string]bool
	imageExts      map[string]bool
	dotWhitelist   map[string]bool
	archivePrefix  string
	ignoreName     string
	largeBytes     int64
	referenced     map[string]bool
	protected      map[string]bool
	protectedNames map[string]bool
	plan           Plan
	ruleChain      []cleanRule
}

func newCategorizer(opts Options, referenced, protected map[string]bool) *categorizer {
	rules := opts.Rules
	c := &categorizer{
		rules:          rules,
		allowedMD:      toSet(rules.AllowedRootMD),
		scaffolds:      toSet(rules.ScaffoldFiles),
		deleteNames:    toSet(rules.DeleteNames),
		deleteExts:     toSet(rules.DeleteExtensions),
		deleteDirs:     toSet(rules.DeleteDirectories),
		archiveExts:    toSet(rules.ArchiveExtensions),
		dataExts:       toSet(rules.DataExtensions),
		untrackExts:    toSet(rules.UntrackExtensions),
		imageExts:      toSet(rules.ImageExtensions),
		dotWhitelist:   toSet(rules.DotfileAllowlist),
		archivePrefix:  slashClean(rules.ArchiveDir),
		ignoreName:     slashClean(rules.IgnoreFile),
		largeBytes:     opts.largeFileBytes(),
		referenced:     referenced,
		protected:      protected,
		protectedNames: toSet(rules.ProtectedNames),
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
