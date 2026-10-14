package reportimport

import (
	"context"
	"fmt"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// wireGuard checks descriptors before generated Unmarshal allocates repeated
// messages or applies protobuf's last-one-wins singular/oneof semantics.
type wireGuard struct {
	ctx                context.Context
	l                  Limits
	records, documents int
	unsupported        []string
}

func (g *wireGuard) message(data []byte, desc protoreflect.MessageDescriptor, depth int, path string) error {
	if err := g.ctx.Err(); err != nil {
		return err
	}
	if depth > g.l.Depth {
		return fmt.Errorf("protobuf nesting limit exceeded")
	}
	seen := map[protoreflect.FieldNumber]bool{}
	oneofs := map[protoreflect.FullName]bool{}
	ordinal := 0
	for len(data) > 0 {
		if err := g.ctx.Err(); err != nil {
			return err
		}
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 || num <= 0 || typ == protowire.StartGroupType || typ == protowire.EndGroupType {
			return fmt.Errorf("invalid or unsupported protobuf tag at %s", path)
		}
		if desc.FullName() == "scip.Index" && ordinal == 0 && num != 1 {
			return fmt.Errorf("SCIP metadata must be first")
		}
		data = data[n:]
		fd := desc.Fields().ByNumber(protoreflect.FieldNumber(num))
		if fd == nil {
			if err := g.count(); err != nil {
				return err
			}
			used := protowire.ConsumeFieldValue(num, typ, data)
			if used < 0 {
				return fmt.Errorf("invalid unknown protobuf field at %s", path)
			}
			g.unsupported = append(g.unsupported, fmt.Sprintf("%s.field[%d]: unknown wire field retained in original protobuf; uninterpreted", path, num))
			data = data[used:]
			ordinal++
			continue
		}
		if !fd.IsList() && seen[fd.Number()] {
			return fmt.Errorf("duplicate singular protobuf field %s.%s", path, fd.Name())
		}
		if one := fd.ContainingOneof(); one != nil {
			if oneofs[one.FullName()] {
				return fmt.Errorf("ambiguous protobuf oneof %s", one.FullName())
			}
			oneofs[one.FullName()] = true
		}
		if desc.FullName() == "scip.Index" {
			if ordinal == 0 && fd.Number() != 1 {
				return fmt.Errorf("SCIP metadata must be first")
			}
			if fd.Number() == 2 {
				g.documents++
				if g.documents > g.l.Documents {
					return fmt.Errorf("SCIP document limit exceeded")
				}
			}
		}
		seen[fd.Number()] = true
		fieldPath := fmt.Sprintf("%s.%s[%d]", path, fd.Name(), ordinal)
		used, err := g.field(data, typ, fd, depth, fieldPath)
		if err != nil {
			return err
		}
		data = data[used:]
		ordinal++
	}
	if desc.FullName() == "scip.Index" && !seen[1] {
		return fmt.Errorf("SCIP metadata is required first and once")
	}
	return nil
}

func (g *wireGuard) count() error {
	if g.records >= g.l.Records {
		return fmt.Errorf("protobuf aggregate record/value limit exceeded")
	}
	g.records++
	return nil
}

func (g *wireGuard) field(data []byte, typ protowire.Type, fd protoreflect.FieldDescriptor, depth int, path string) (int, error) {
	if err := g.count(); err != nil {
		return 0, err
	}
	if typ == protowire.BytesType {
		payload, n := protowire.ConsumeBytes(data)
		if n < 0 {
			return 0, fmt.Errorf("malformed protobuf bytes at %s", path)
		}
		switch fd.Kind() {
		case protoreflect.MessageKind:
			if err := g.message(payload, fd.Message(), depth+1, path); err != nil {
				return 0, err
			}
		case protoreflect.StringKind:
			if len(payload) > g.l.StringBytes || !utf8.Valid(payload) {
				return 0, fmt.Errorf("invalid UTF-8 or excessive protobuf string at %s", path)
			}
			if fd.FullName() == "scip.Document.relative_path" && len(payload) > g.l.PathBytes {
				return 0, fmt.Errorf("SCIP path byte limit exceeded")
			}
		case protoreflect.BytesKind:
			if len(payload) > g.l.StringBytes {
				return 0, fmt.Errorf("protobuf byte string limit exceeded")
			}
		default:
			if !fd.IsList() || !fd.IsPacked() {
				return 0, fmt.Errorf("unexpected protobuf bytes type at %s", path)
			}
			count := 0
			for len(payload) > 0 {
				if count > 0 {
					if err := g.count(); err != nil {
						return 0, err
					}
				}
				u, err := g.scalar(payload, scalarWire(fd.Kind()), fd, path)
				if err != nil {
					return 0, err
				}
				payload = payload[u:]
				count++
				if fd.ContainingMessage().FullName() == "scip.Occurrence" && (fd.Number() == 1 || fd.Number() == 7) && count > 4 {
					return 0, fmt.Errorf("SCIP range cardinality exceeds four")
				}
			}
		}
		return n, nil
	}
	return g.scalar(data, typ, fd, path)
}
