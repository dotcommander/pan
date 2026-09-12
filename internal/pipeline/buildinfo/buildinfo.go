// Package buildinfo exposes the running binary's VCS and toolchain stamp, read
// from the Go build info embedded at link time. It is the single source of
// truth for detecting when an installed binary has drifted from source.
package buildinfo

import "runtime/debug"

// Info is the build provenance of the running binary.
type Info struct {
	GoVersion     string `json:"go_version"`
	ModulePath    string `json:"module_path"`
	ModuleVersion string `json:"module_version"`
	VCSRevision   string `json:"vcs_revision"`
	VCSTime       string `json:"vcs_time"`
	VCSModified   bool   `json:"vcs_modified"`
}

// Read returns the embedded build info. Fields are best-effort: a binary built
// with `go build` in a clean checkout fills the vcs.* settings; `go run` and
// some test binaries leave them empty.
func Read() Info {
	info := Info{}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	info.GoVersion = bi.GoVersion
	info.ModulePath = bi.Main.Path
	info.ModuleVersion = bi.Main.Version
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.VCSRevision = s.Value
		case "vcs.time":
			info.VCSTime = s.Value
		case "vcs.modified":
			info.VCSModified = s.Value == "true"
		}
	}
	return info
}
