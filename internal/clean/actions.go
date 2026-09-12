package clean

import (
	"path"
	"strings"
)

// ActionKind labels how an action is executed. Filesystem kinds run through
// the Go standard library; KindGit runs one direct argv git command. No
// action is ever executed through a shell.
type ActionKind string

// ActionKind values for planned cleanup steps.
const (
	KindMkdir  ActionKind = "mkdir"
	KindRemove ActionKind = "remove"
	KindMove   ActionKind = "move"
	KindGit    ActionKind = "git"
	KindReview ActionKind = "review"
)

// argv vocabulary for the copy-paste review lines. Execution never passes
// these through a shell; they only build direct argv vectors.
const (
	argSeparator = "--"
	cmdMkdir     = "mkdir"
	cmdMove      = "mv"
	cmdRemove    = "rm"
	cmdGit       = "git"
)

// Action is one planned cleanup step. Argv and Display are advisory,
// copy-paste-ready shell representations; execution uses Source and Target
// through safeJoin, never the display string.
type Action struct {
	Category string     `json:"category"`
	Kind     ActionKind `json:"kind"`
	Argv     []string   `json:"argv,omitempty"`
	Display  string     `json:"display"`
	Comment  bool       `json:"comment,omitempty"` // manual review; never executed
	Source   string     `json:"source,omitempty"`  // slash path relative to root
	Target   string     `json:"target,omitempty"`  // slash path relative to root
}

// CommandGroup batches the actions of one category.
type CommandGroup struct {
	Category string   `json:"category"`
	Commands []Action `json:"commands"`
}

// CommandsReport is the `clean commands` result: every planned action as
// copy-paste shell lines, grouped by category. Pan never executes them.
type CommandsReport struct {
	Schema string         `json:"schema"`
	Path   string         `json:"path"`
	Total  int            `json:"total"`
	Groups []CommandGroup `json:"groups"`
	Note   string         `json:"note,omitempty"`
}

// actionBuilder accumulates ordered actions while deduplicating directory
// creations.
type actionBuilder struct {
	actions  []Action
	seenDirs map[string]bool
}

func newActionBuilder() *actionBuilder {
	return &actionBuilder{seenDirs: make(map[string]bool)}
}

// mkdir records one directory creation unless it was already planned.
func (b *actionBuilder) mkdir(category, dir string) {
	if dir == "" || dir == "." || b.seenDirs[dir] {
		return
	}
	b.seenDirs[dir] = true
	argv := []string{cmdMkdir, "-p", argSeparator, dir}
	b.actions = append(b.actions, Action{
		Category: category, Kind: KindMkdir,
		Argv: argv, Display: shellJoin(argv), Target: dir,
	})
}

// move records one filesystem move.
func (b *actionBuilder) move(category, src, dst string) {
	b.mkdir(category, path.Dir(dst))
	argv := []string{cmdMove, argSeparator, src, dst}
	b.actions = append(b.actions, Action{
		Category: category, Kind: KindMove,
		Argv: argv, Display: shellJoin(argv), Source: src, Target: dst,
	})
}

// gitAction records one direct-argv git command.
func (b *actionBuilder) gitAction(category string, argv []string) {
	b.actions = append(b.actions, Action{
		Category: category, Kind: KindGit, Argv: argv,
		Display: shellJoin(argv), Source: argv[len(argv)-1],
	})
}

// remove records one filesystem removal.
func (b *actionBuilder) remove(c Candidate, recursive bool) {
	flag := "-f"
	if recursive {
		flag = "-rf"
	}
	argv := []string{cmdRemove, flag, argSeparator, c.File}
	b.actions = append(b.actions, Action{
		Category: "delete", Kind: KindRemove, Argv: argv,
		Display: shellJoin(argv), Target: c.File,
	})
}

// BuildActions turns a plan into ordered, executable actions. Untracked
// paths only ever receive filesystem moves; git index operations (git mv,
// git rm --cached) are reserved for tracked files.
func BuildActions(plan Plan, opts Options) []Action {
	b := newActionBuilder()
	archiveRoot := path.Join(slashClean(opts.Rules.ArchiveDir), opts.now().Format("2006-01-02"))
	b.appendDeletes(plan.DeleteCandidates)
	b.appendDevArtifacts(plan.DevArtifactCandidates, archiveRoot)
	b.appendArchives(plan.ArchiveCandidates, archiveRoot)
	b.appendMisplacedDocs(plan.MisplacedDocs)
	b.appendMisplacedScripts(plan.MisplacedScripts, archiveRoot)
	b.appendRenameDocs(plan.RenameDocs)
	b.appendUntracks(plan.UntrackCandidates)
	b.appendReviews(plan.LargeFiles, plan.BrokenLinks)
	return b.actions
}

func (b *actionBuilder) appendDeletes(candidates []Candidate) {
	for _, c := range candidates {
		b.remove(c, strings.HasSuffix(c.Reason, "directory"))
	}
}

func (b *actionBuilder) appendDevArtifacts(candidates []Candidate, archiveRoot string) {
	for _, c := range candidates {
		// Tracked scratch: untrack from the index, then move on disk.
		b.gitAction("dev_artifact", []string{cmdGit, "rm", "--cached", argSeparator, c.File})
		b.move("dev_artifact", c.File, path.Join(archiveRoot, c.File))
	}
}

func (b *actionBuilder) appendArchives(candidates []Candidate, archiveRoot string) {
	for _, c := range candidates {
		b.move("archive", c.File, path.Join(archiveRoot, archiveDestName(c.File)))
	}
}

func (b *actionBuilder) appendMisplacedDocs(candidates []Candidate) {
	for _, c := range candidates {
		dst := path.Join("docs", c.File)
		if tracked(c) {
			b.mkdir("misplaced_docs", "docs")
			b.gitAction("misplaced_docs", []string{cmdGit, cmdMove, argSeparator, c.File, dst})
		} else {
			b.move("misplaced_docs", c.File, dst)
		}
	}
}

func (b *actionBuilder) appendMisplacedScripts(candidates []Candidate, archiveRoot string) {
	for _, c := range candidates {
		if c.Referenced != nil && *c.Referenced {
			b.appendReferencedScript(c)
			continue
		}
		b.move("misplaced_scripts", c.File, path.Join(archiveRoot, archiveDestName(c.File)))
	}
}

func (b *actionBuilder) appendReferencedScript(c Candidate) {
	dst := path.Join("scripts", c.File)
	if tracked(c) {
		b.mkdir("misplaced_scripts", "scripts")
		b.gitAction("misplaced_scripts", []string{cmdGit, cmdMove, argSeparator, c.File, dst})
		return
	}
	b.move("misplaced_scripts", c.File, dst)
}

func (b *actionBuilder) appendRenameDocs(candidates []Candidate) {
	for _, c := range candidates {
		if tracked(c) {
			b.gitAction("rename_docs", []string{cmdGit, cmdMove, argSeparator, c.File, c.Target})
		} else {
			b.move("rename_docs", c.File, c.Target)
		}
	}
}

func (b *actionBuilder) appendUntracks(candidates []Candidate) {
	for _, c := range candidates {
		b.gitAction("untrack", []string{cmdGit, "rm", "--cached", argSeparator, c.File})
	}
}

func (b *actionBuilder) appendReviews(large, broken []Candidate) {
	for _, c := range large {
		b.actions = append(b.actions, Action{
			Category: "review", Kind: KindReview, Comment: true,
			Display: "# manual review: large file " + c.File + " (" + fmtSizeKB(c.SizeKB) + ")",
		})
	}
	for _, c := range broken {
		b.actions = append(b.actions, Action{
			Category: "review", Kind: KindReview, Comment: true,
			Display: "# manual review: broken symlink " + c.File + " → " + c.Target,
		})
	}
}

// tracked resolves the candidate's tracked pointer; nil means unknown and
// is treated as untracked so no git index mutation is ever attempted on an
// uncertain path.
func tracked(c Candidate) bool { return c.Tracked != nil && *c.Tracked }

// archiveDestName flattens escaping archive destinations: a candidate path
// that is empty, absolute, or parent-escaping archives under its base name.
func archiveDestName(rel string) string {
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == parentDir || strings.HasPrefix(cleaned, parentDir+"/") || path.IsAbs(cleaned) {
		return path.Base(rel)
	}
	return cleaned
}

// BuildCommandsReport groups actions into the `clean commands` result.
func BuildCommandsReport(root string, actions []Action) CommandsReport {
	report := CommandsReport{Schema: CommandsSchema, Path: root, Note: "commands are a review artifact; pan never executes them"}
	var order []string
	byCategory := map[string]*CommandGroup{}
	for _, a := range actions {
		group, ok := byCategory[a.Category]
		if !ok {
			group = &CommandGroup{Category: a.Category}
			byCategory[a.Category] = group
			order = append(order, a.Category)
		}
		group.Commands = append(group.Commands, a)
		if !a.Comment {
			report.Total++
		}
	}
	for _, category := range order {
		report.Groups = append(report.Groups, *byCategory[category])
	}
	return report
}

// shellQuote quotes one shell word.
func shellQuote(word string) string {
	if strings.ContainsAny(word, " \t'\"\\$`!#&|;(){}[]<>?*~") {
		return "'" + strings.ReplaceAll(word, "'", "'\\''") + "'"
	}
	return word
}

// shellJoin renders an argv as one copy-paste shell line.
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

// fmtSizeKB renders a KB value for review comments.
func fmtSizeKB(kb int64) string { return fmtSize(kb * 1024) }
