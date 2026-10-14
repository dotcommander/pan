// Package pack transforms captured Repomix content without treating it as source.
package pack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"unicode/utf8"
)

// Limits bounds input, decoded content, inventory, nesting and rendered output.
// Zero fields select the corresponding default; negative fields are invalid.
type Limits struct {
	InputBytes, DecodedBytes, FileBytes, OutputBytes int64
	Files, PathBytes, Depth                          int
}

func (l Limits) resolved() (Limits, error) {
	defaults := Limits{64 << 20, 64 << 20, 16 << 20, 256 << 20, 100000, 4096, 128}
	values := []*int64{&l.InputBytes, &l.DecodedBytes, &l.FileBytes, &l.OutputBytes}
	base := []int64{defaults.InputBytes, defaults.DecodedBytes, defaults.FileBytes, defaults.OutputBytes}
	for i, p := range values {
		if *p < 0 || *p == int64(^uint64(0)>>1) {
			return l, fmt.Errorf("invalid pack byte limit")
		}
		if *p == 0 {
			*p = base[i]
		}
	}
	ints := []*int{&l.Files, &l.PathBytes, &l.Depth}
	for i, v := range []int{defaults.Files, defaults.PathBytes, defaults.Depth} {
		if *ints[i] < 0 {
			return l, fmt.Errorf("invalid pack count limit")
		}
		if *ints[i] == 0 {
			*ints[i] = v
		}
	}
	// Parsing uses bounded recursion; callers may tighten but never raise this cap.
	if l.Depth > defaults.Depth {
		return l, fmt.Errorf("pack depth exceeds supported maximum")
	}
	return l, nil
}

// File retains a path and either inline content or an index into Document.Shared.
// Content is a pointer so an empty file remains distinguishable from a reference.
type File struct {
	Path    string  `json:"path"`
	Content *string `json:"content,omitempty"`
	Ref     *int    `json:"ref,omitempty"`
}

// Document is the pan-pack/v1 wire format. References resolve to Shared[ref].
type Document struct {
	Schema   string                     `json:"schema"`
	Input    Input                      `json:"input"`
	Coverage Coverage                   `json:"coverage"`
	Fidelity Fidelity                   `json:"fidelity"`
	Metadata map[string]json.RawMessage `json:"metadata,omitempty"`
	Files    []File                     `json:"files"`
	Shared   []string                   `json:"shared,omitempty"`
}

type Input struct {
	Format   string `json:"format"`
	SHA256   string `json:"sha256"`
	Bytes    int    `json:"bytes"`
	Producer string `json:"producer"`
}

type Coverage struct {
	Captured       int    `json:"captured"`
	Retained       int    `json:"retained"`
	Scope          string `json:"scope"`
	FullRepository string `json:"full_repository"`
	Exclusions     string `json:"exclusions"`
}

type Fidelity struct {
	OriginalSource   bool     `json:"original_source"`
	OriginalLines    bool     `json:"original_lines"`
	AtomicSnapshot   bool     `json:"atomic_snapshot"`
	InputCompression string   `json:"input_compression"`
	InheritedLoss    string   `json:"inherited_loss"`
	OmittedEnvelope  []string `json:"omitted_envelope,omitempty"`
	JSONCompacted    []string `json:"json_compacted,omitempty"`
	JSONDepthSkipped []string `json:"json_depth_skipped,omitempty"`
}

type imported struct {
	format   string
	files    map[string]string
	metadata map[string]json.RawMessage
	omitted  []string
}

// Build reads only the explicit stream. Packed paths are identifiers, never IO.
func Build(ctx context.Context, input io.Reader, limits Limits) ([]byte, error) {
	l, err := limits.resolved()
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("pack input stream is missing")
	}
	raw, err := io.ReadAll(io.LimitReader(contextReader{ctx, input}, l.InputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read pack: %w", err)
	}
	if int64(len(raw)) > l.InputBytes {
		return nil, fmt.Errorf("pack input exceeds byte limit %d", l.InputBytes)
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("pack input must be valid UTF-8")
	}
	parsed, err := importPack(ctx, raw, l)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	doc := Document{
		Schema:   "pan-pack/v1",
		Input:    Input{parsed.format, hex.EncodeToString(digest[:]), len(raw), "unverified_repomix_envelope"},
		Coverage: Coverage{len(parsed.files), len(parsed.files), "captured_inventory", "unknown", "unknown_see_metadata"},
		Fidelity: Fidelity{InputCompression: "unknown_see_metadata", InheritedLoss: "unknown_see_metadata", OmittedEnvelope: parsed.omitted},
		Metadata: parsed.metadata,
		Files:    make([]File, 0, len(parsed.files)),
	}
	paths := make([]string, 0, len(parsed.files))
	for path := range parsed.files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		content, changed, deep := normalize(parsed.files[path], l.Depth)
		if changed {
			doc.Fidelity.JSONCompacted = append(doc.Fidelity.JSONCompacted, path)
		}
		if deep {
			doc.Fidelity.JSONDepthSkipped = append(doc.Fidelity.JSONDepthSkipped, path)
		}
		doc.Files = append(doc.Files, File{Path: path, Content: &content})
	}
	output, err := render(doc)
	if err != nil {
		return nil, err
	}
	if int64(len(output))+1 > l.OutputBytes {
		return nil, fmt.Errorf("pack output exceeds byte limit %d", l.OutputBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append(output, '\n'), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
