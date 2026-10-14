package reportimport

import (
	"fmt"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func scalarWire(kind protoreflect.Kind) protowire.Type {
	switch kind {
	case protoreflect.DoubleKind, protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return protowire.Fixed64Type
	case protoreflect.FloatKind, protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		return protowire.Fixed32Type
	default:
		return protowire.VarintType
	}
}

func (g *wireGuard) scalar(data []byte, typ protowire.Type, fd protoreflect.FieldDescriptor, path string) (int, error) {
	if fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.StringKind || fd.Kind() == protoreflect.BytesKind || typ != scalarWire(fd.Kind()) {
		return 0, fmt.Errorf("wrong protobuf wire type at %s", path)
	}
	var value uint64
	var n int
	switch typ {
	case protowire.VarintType:
		value, n = protowire.ConsumeVarint(data)
	case protowire.Fixed32Type:
		_, n = protowire.ConsumeFixed32(data)
	case protowire.Fixed64Type:
		_, n = protowire.ConsumeFixed64(data)
	default:
		return 0, fmt.Errorf("unsupported scalar wire type")
	}
	if n < 0 {
		return 0, fmt.Errorf("malformed protobuf scalar at %s", path)
	}
	if typ == protowire.VarintType {
		switch fd.Kind() {
		case protoreflect.Int32Kind, protoreflect.EnumKind:
			if uint64(int64(int32(value))) != value {
				return 0, fmt.Errorf("overflowing int32 at %s", path)
			}
		case protoreflect.Uint32Kind, protoreflect.Sint32Kind:
			if value > 1<<32-1 {
				return 0, fmt.Errorf("overflowing uint32 at %s", path)
			}
		case protoreflect.BoolKind:
			if value > 1 {
				return 0, fmt.Errorf("non-boolean protobuf value at %s", path)
			}
		}
		if fd.Kind() == protoreflect.EnumKind && fd.Enum().Values().ByNumber(protoreflect.EnumNumber(int32(value))) == nil {
			g.unsupported = append(g.unsupported, fmt.Sprintf("%s: unknown enum %d retained numerically; uninterpreted", path, int32(value)))
		}
	}
	return n, nil
}
