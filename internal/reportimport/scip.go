package reportimport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const scipSchema = "https://github.com/scip-code/scip/blob/fcb3cb3c679fe2da18141550f864b9e67afab297/scip.proto"

func importSCIP(ctx context.Context, data []byte, l Limits, r *Report) error {
	index := new(scip.Index)
	guard := wireGuard{ctx: ctx, l: l}
	if err := guard.message(data, index.ProtoReflect().Descriptor(), 0, "index"); err != nil {
		return err
	}
	if err := (proto.UnmarshalOptions{RecursionLimit: l.Depth}).Unmarshal(data, index); err != nil {
		return fmt.Errorf("decode SCIP: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.Origin.SchemaReference = scipSchema
	if tool := index.GetMetadata().GetToolInfo(); tool != nil {
		if tool.GetName() != "" {
			name := tool.GetName()
			r.Origin.Producer = &name
		}
		if tool.GetVersion() != "" {
			version := tool.GetVersion()
			r.Origin.Version = &version
		}
	}
	r.Unsupported = append(r.Unsupported, guard.unsupported...)
	r.Coordinates = "SCIP zero-based half-open; per-document position_encoding retained; unspecified/unknown encodings uninterpreted"
	r.Limitations = append(r.Limitations, "Known SCIP fields projected using pinned official schema; exact original native bytes retained for unknown fields and wire identity.", "Definition, forward-definition, import and access role bits retain SCIP meanings; local symbols are document-scoped. No source capture or freshness check performed.")
	paths := map[string]bool{}
	for di, doc := range index.GetDocuments() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validPath(doc.GetRelativePath(), true, l); err != nil {
			return err
		}
		if paths[doc.GetRelativePath()] {
			return fmt.Errorf("duplicate SCIP document path %q", doc.GetRelativePath())
		}
		paths[doc.GetRelativePath()] = true
		encoding := doc.GetPositionEncoding().String()
		if doc.GetPositionEncoding() == scip.PositionEncoding_UnspecifiedPositionEncoding {
			encoding = "unknown (unspecified)"
		}
		for oi, occ := range doc.GetOccurrences() {
			view, err := scipOccurrence(occ, r)
			if err != nil {
				return fmt.Errorf("document %s occurrence %d: %w", doc.GetRelativePath(), oi, err)
			}
			view.Path = doc.GetRelativePath()
			view.Ordinal = oi
			ordinal := di
			view.DocumentOrdinal = &ordinal
			view.PositionEncoding = encoding
			if strings.HasPrefix(view.Symbol, "local ") {
				view.LocalNamespace = doc.GetRelativePath()
			}
			r.Views = append(r.Views, view)
		}
		for _, symbol := range doc.GetSymbols() {
			if err := validateSCIPSymbol(symbol, r); err != nil {
				return err
			}
		}
	}
	for _, symbol := range index.GetExternalSymbols() {
		if err := validateSCIPSymbol(symbol, r); err != nil {
			return err
		}
	}
	// Original wire bytes are also emitted. Account for their base64 size before
	// allocating either projection or base64 output.
	sizer := jsonSizer{ctx: ctx, left: l.OutputBytes}
	if err := sizer.add(base64.StdEncoding.EncodedLen(len(data))); err != nil {
		return err
	}
	if err := sizer.protoMessage(index.ProtoReflect(), 0); err != nil {
		return err
	}
	projection, err := (protojson.MarshalOptions{UseProtoNames: true, UseEnumNumbers: true}).Marshal(index)
	if err != nil {
		return fmt.Errorf("project SCIP JSON: %w", err)
	}
	// Protojson deliberately varies whitespace and map traversal. Decode only its
	// known projection with UseNumber, then sort object keys via encoding/json.
	decoder := json.NewDecoder(bytes.NewReader(projection))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return err
	}
	r.Evidence, err = json.Marshal(normalized)
	if err != nil {
		return err
	}
	r.OriginalBase64 = base64.StdEncoding.EncodeToString(data)
	r.Import.Documents = len(index.GetDocuments())
	r.Import.Records = len(r.Views) + len(index.GetExternalSymbols())
	return nil
}

func validateSCIPSymbol(symbol *scip.SymbolInformation, r *Report) error {
	if symbol.GetSymbol() == "" {
		return fmt.Errorf("SCIP symbol information missing identity")
	}
	for _, rel := range symbol.GetRelationships() {
		if rel.GetSymbol() == "" {
			return fmt.Errorf("SCIP relationship missing identity")
		}
	}
	if signature := symbol.GetSignatureDocumentation(); signature != nil {
		for _, occ := range signature.GetOccurrences() {
			if _, err := scipOccurrence(occ, r); err != nil {
				return fmt.Errorf("signature occurrence: %w", err)
			}
		}
	}
	return nil
}
