package cli

import "sort"

type detailLevel string

const (
	detailCompact  detailLevel = "compact"
	detailEvidence detailLevel = "evidence"
	detailPaths    detailLevel = "paths"
)

func (d detailLevel) valid() bool {
	return d == detailCompact || d == detailEvidence || d == detailPaths
}

func normalizeDetail(d detailLevel) detailLevel {
	if !d.valid() {
		return detailCompact
	}
	return d
}

func sortedStrings(values []string) []string {
	values = append([]string{}, values...)
	sort.Strings(values)
	return values
}
