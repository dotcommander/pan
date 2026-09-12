package improve

// PrepContext preserves coverage-lift evidence in source-compatible history.
type PrepContext struct {
	AlreadySufficient bool                `json:"already_sufficient"`
	BaselineCoverage  float64             `json:"baseline_coverage"`
	FinalCoverage     float64             `json:"final_coverage"`
	CoverageDelta     float64             `json:"coverage_delta"`
	CoverageIncreased bool                `json:"coverage_increased"`
	TestsAdded        int                 `json:"tests_added"`
	Targets           []PrepTargetContext `json:"targets,omitempty"`
}

// PrepTargetContext records one prepared test target and its coverage evidence.
type PrepTargetContext struct {
	RepoRelFile       string  `json:"repo_rel_file"`
	ModQualFile       string  `json:"mod_qual_file,omitempty"`
	Outcome           string  `json:"outcome"`
	Round             int     `json:"round,omitempty"`
	WorklistRank      int     `json:"worklist_rank,omitempty"`
	CoverageIncreased bool    `json:"coverage_increased,omitempty"`
	Reason            string  `json:"reason,omitempty"`
	Score             float64 `json:"score,omitempty"`
	EstimatedLift     float64 `json:"estimated_lift,omitempty"`
	BeforeCoverage    float64 `json:"before_coverage,omitempty"`
	AfterCoverage     float64 `json:"after_coverage,omitempty"`
	CoverageDelta     float64 `json:"coverage_delta,omitempty"`
	TestMillis        int64   `json:"test_millis,omitempty"`
	Provisional       bool    `json:"provisional,omitempty"`
	ArtifactPath      string  `json:"artifact_path,omitempty"`
}

// RefactorContext records timing and selection facts for a guarded refactor.
type RefactorContext struct {
	PackagePreflightMillis int64  `json:"package_preflight_millis,omitempty"`
	FullSuiteMillis        int64  `json:"full_suite_millis,omitempty"`
	CandidateSource        string `json:"candidate_source,omitempty"`
}

// TargetContext records the analysis surfaces used to select a refactor target.
type TargetContext struct {
	Source          string   `json:"source,omitempty"`
	RiskLanes       []string `json:"risk_lanes,omitempty"`
	SurfaceKinds    []string `json:"surface_kinds,omitempty"`
	EffectKinds     []string `json:"effect_kinds,omitempty"`
	FirstReadGroups []string `json:"first_read_groups,omitempty"`
	ReviewVerify    []string `json:"review_verify,omitempty"`
}

// CandidatePacket records the bounded shortlist supplied to a refactor workflow.
type CandidatePacket struct {
	Files          []CandidateFile `json:"files,omitempty"`
	SkippedSignals []string        `json:"skipped_signals,omitempty"`
}

// CandidateFile records evidence and verification guidance for one shortlist file.
type CandidateFile struct {
	Path           string   `json:"path"`
	Lane           string   `json:"lane,omitempty"`
	Confidence     string   `json:"confidence,omitempty"`
	Actionability  string   `json:"actionability,omitempty"`
	EvidenceLayers []string `json:"evidence_layers,omitempty"`
	Reasons        []string `json:"reasons,omitempty"`
	Verify         []string `json:"verify,omitempty"`
}
