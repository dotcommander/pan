package clean

// ruleWeights is the per-severity score contribution of one rule.
type ruleWeights struct {
	info int
	warn int
	err  int
}

// weightsFor maps a rule onto its per-severity score contributions.
func weightsFor(rule string) (ruleWeights, bool) {
	switch rule {
	case RuleUntracked:
		return ruleWeights{20, 20, 20}, true
	case RuleStale:
		return ruleWeights{15, 25, 35}, true
	case RuleLargeFile:
		return ruleWeights{0, 10, 20}, true
	case RuleGenerated:
		return ruleWeights{15, 15, 15}, true
	case RuleScratch:
		return ruleWeights{20, 20, 20}, true
	case RuleTodoOnly:
		return ruleWeights{20, 20, 20}, true
	case RuleLogDump:
		return ruleWeights{10, 10, 10}, true
	case RuleOrphaned:
		return ruleWeights{25, 25, 25}, true
	case RuleDuplicate:
		return ruleWeights{20, 20, 20}, true
	case RuleEmpty:
		return ruleWeights{30, 30, 30}, true
	default:
		return ruleWeights{}, false
	}
}

// bySeverity resolves the contribution for one severity value, clamping
// out-of-range severities to the nearest documented level.
func (w ruleWeights) bySeverity(severity int) int {
	switch {
	case severity <= SevInfo:
		return w.info
	case severity == SevWarn:
		return w.warn
	default:
		return w.err
	}
}

// Score returns a 0-100 cleanup confidence score for one file based on its
// findings.
func Score(f *FileInfo) int {
	score := 0
	for _, finding := range f.Findings {
		w, ok := weightsFor(finding.Rule)
		if !ok {
			continue
		}
		score += w.bySeverity(finding.Severity)
	}
	return min(score, 100)
}

// CalculateHealth returns a 0-100 repository health score. Higher is
// better; 100 means zero cleanup candidates. It blends the clean-file
// ratio, severity-weighted penalties, and a bounded size penalty.
func CalculateHealth(plan Plan) int {
	totalFiles := 0
	cleanFiles := 0
	for _, f := range plan.AllFiles {
		totalFiles++
		if f.Status == StatusClean {
			cleanFiles++
		}
	}
	if totalFiles == 0 {
		return 100
	}

	health := (cleanFiles * 100) / totalFiles
	health = (health*40)/100 + 60

	deductions := len(plan.BrokenLinks) * 5
	deductions += len(plan.LargeFiles) * 3
	deductions += len(plan.UntrackCandidates) * 2
	deductions += len(plan.DeleteCandidates)
	deductions = min(deductions, 40)
	health -= deductions

	var junkKB int64
	for _, c := range plan.DeleteCandidates {
		junkKB += c.SizeKB
	}
	for _, c := range plan.ArchiveCandidates {
		junkKB += c.SizeKB
	}
	health -= min(int(junkKB/(50*1024)), 20)

	return max(health, 0)
}
