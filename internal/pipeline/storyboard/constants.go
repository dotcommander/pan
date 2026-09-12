package storyboard

// Stage roles used across the storyboard model, converters, and renderers.
const (
	roleEntry   = "entry"
	roleRoute   = "route"
	roleParse   = "parse"
	roleAnalyze = "analyze"
	roleRender  = "render"
	roleWatch   = "watch"
	roleServe   = "serve"
	roleWrite   = "write"
	roleIO      = "io"
	roleOther   = "other"
)

// Store access classifications shared by converters and renderers.
const (
	accessRead  = "read"
	accessWrite = "write"
)

// Diff areas identify which storyboard section produced a diff item.
const (
	areaScan    = "scan"
	areaCompare = "compare"
)

// laneScan names the scan command lane that drives shared-pipeline detection.
const laneScan = "scan"

// severityHigh mirrors the review engine's high severity label.
const severityHigh = "high"

const statusMissing = "missing"

const (
	commandHelpCompletion  = "completion"
	commandHelpHelp        = "help"
	commandStoryboard      = "storyboard"
	commandHelpUsage       = "Usage:"
	commandHelpAvailable   = "Available Commands:"
	commandHelpSubcommands = "Subcommands:"
	goRunSubcommand        = "run"
)
