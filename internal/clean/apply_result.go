package clean

// ApplyResult is the `clean apply` result for both dry-runs and confirmed
// runs. A confirmed run additionally records the backup and manifest paths.
type ApplyResult struct {
	Coverage  `json:"-"`
	Schema    string         `json:"schema"`
	Path      string         `json:"path"`
	Confirmed bool           `json:"confirmed"`
	Backup    string         `json:"backup,omitempty"`
	Manifest  string         `json:"manifest,omitempty"`
	BackedUp  int            `json:"backed_up"`
	Counts    ApplyCounts    `json:"counts"`
	Actions   []ActionResult `json:"actions,omitempty"`
	Note      string         `json:"note,omitempty"`
}
