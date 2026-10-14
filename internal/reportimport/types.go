// Package reportimport imports inert upstream evidence without source analysis.
package reportimport

import "encoding/json"

// Limits bound input, intermediate records and the rendered result. Zero selects
// the documented default. They are injectable so failure tests stay small.
type Limits struct {
	OutputBytes                                       int
	InputBytes                                        int64
	Depth, Records, Documents, StringBytes, PathBytes int
}

func (l Limits) defaults() Limits {
	if l.OutputBytes <= 0 {
		l.OutputBytes = 256 << 20
	}
	if l.InputBytes <= 0 {
		l.InputBytes = 64 << 20
	}
	if l.Depth <= 0 {
		l.Depth = 64
	}
	if l.Records <= 0 {
		l.Records = 100000
	}
	if l.Documents <= 0 {
		l.Documents = 10000
	}
	if l.StringBytes <= 0 {
		l.StringBytes = 8 << 20
	}
	if l.PathBytes <= 0 {
		l.PathBytes = 16384
	}
	return l
}

// Report separates complete parsing of the captured input from source coverage.
// Evidence retains upstream order. Views are stable indexes, never new findings.
type Report struct {
	Schema         string          `json:"schema"`
	Origin         Origin          `json:"origin"`
	Import         ImportStatus    `json:"import"`
	Source         SourceStatus    `json:"source"`
	Coordinates    string          `json:"coordinates"`
	Ranking        string          `json:"ranking"`
	Limitations    []string        `json:"limitations"`
	Unsupported    []string        `json:"unsupported"`
	Evidence       json.RawMessage `json:"evidence"`
	Views          []View          `json:"views"`
	OriginalBase64 string          `json:"original_protobuf_base64,omitempty"`
}

type Origin struct {
	Format          string  `json:"format"`
	SchemaReference string  `json:"schema_reference"`
	Producer        *string `json:"producer"`
	Version         *string `json:"version"`
	InputSHA256     string  `json:"input_sha256"`
	InputBytes      int     `json:"input_bytes"`
}

type ImportStatus struct {
	Complete  bool   `json:"complete"`
	Scope     string `json:"scope"`
	Records   int    `json:"records"`
	Documents int    `json:"documents"`
}

type SourceStatus struct {
	Coverage   string `json:"coverage"`
	Freshness  string `json:"freshness"`
	Exclusions string `json:"exclusions"`
}

// View points back into retained source-order evidence by zero-based ordinal.
// SCIP local symbols are namespaced by document path, never globally unified.
type View struct {
	Path             string  `json:"path"`
	Ordinal          int     `json:"ordinal"`
	DocumentOrdinal  *int    `json:"document_ordinal,omitempty"`
	OtherPath        string  `json:"other_path,omitempty"`
	Symbol           string  `json:"symbol,omitempty"`
	LocalNamespace   string  `json:"local_namespace,omitempty"`
	Roles            int32   `json:"roles,omitempty"`
	Range            []int64 `json:"range,omitempty"`
	EnclosingRange   []int64 `json:"enclosing_range,omitempty"`
	PositionEncoding string  `json:"position_encoding,omitempty"`
}
