package reportimport

import (
	"strconv"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Account for the known projection before protojson creates its first output
// buffer. HTML-escaped strings also cover the subsequent canonical JSON pass.
func (s *jsonSizer) protoMessage(m protoreflect.Message, depth int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := s.add(2); err != nil {
		return err
	}
	ordinal := 0
	var failure error
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if ordinal > 0 {
			failure = s.add(2)
		}
		ordinal++
		if failure == nil {
			failure = s.str(string(fd.Name()))
		}
		if failure == nil {
			failure = s.add(2)
		}
		if failure == nil {
			if fd.IsList() {
				list := v.List()
				// protojson may insert one extra space after each comma.
				failure = s.add(list.Len())
				if failure != nil {
					return false
				}
				failure = s.container(list.Len(), depth, func(i int) error { return s.protoValue(fd, list.Get(i), depth+1) })
			} else {
				failure = s.protoValue(fd, v, depth+1)
			}
		}
		return failure == nil
	})
	return failure
}
func (s *jsonSizer) protoValue(fd protoreflect.FieldDescriptor, v protoreflect.Value, depth int) error {
	switch fd.Kind() {
	case protoreflect.MessageKind:
		return s.protoMessage(v.Message(), depth)
	case protoreflect.StringKind:
		return s.str(v.String())
	case protoreflect.BytesKind:
		return s.add(2 + 4*((len(v.Bytes())+2)/3))
	case protoreflect.BoolKind:
		if v.Bool() {
			return s.add(4)
		}
		return s.add(5)
	case protoreflect.EnumKind:
		return s.add(len(strconv.FormatInt(int64(v.Enum()), 10)))
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return s.add(len(strconv.FormatInt(v.Int(), 10)))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return s.add(2 + len(strconv.FormatInt(v.Int(), 10)))
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return s.add(2 + len(strconv.FormatUint(v.Uint(), 10)))
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return s.add(len(strconv.FormatUint(v.Uint(), 10)))
	default:
		return s.add(32)
	}
}
