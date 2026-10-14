package reportimport

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// CheckJSONSize accounts for encoding/json's HTML escaping and two-space
// indentation before it can allocate a complete encoded value. It is limited
// to the plain structs/maps/slices used by this importer, not arbitrary codecs.
func CheckJSONSize(ctx context.Context, value any, limit int, indent bool) error {
	s := jsonSizer{ctx: ctx, left: limit, indent: indent}
	return s.value(reflect.ValueOf(value), 0)
}

type jsonSizer struct {
	ctx    context.Context
	left   int
	indent bool
}

func (s *jsonSizer) add(n int) error {
	if n > s.left {
		return fmt.Errorf("encoded report exceeds output byte limit")
	}
	s.left -= n
	return nil
}
func (s *jsonSizer) line(depth int) error {
	if !s.indent {
		return nil
	}
	return s.add(1 + depth*2)
}
func (s *jsonSizer) str(text string) error {
	if err := s.add(2); err != nil {
		return err
	}
	for i := 0; i < len(text); {
		if i%4096 == 0 {
			if err := s.ctx.Err(); err != nil {
				return err
			}
		}
		n := 1
		b := text[i]
		switch {
		case b == '"' || b == '\\' || b == '\n' || b == '\r' || b == '\t' || b == '\b' || b == '\f':
			n = 2
		case b < 0x20 || b == '<' || b == '>' || b == '&':
			n = 6
		case b >= utf8.RuneSelf:
			r, size := utf8.DecodeRuneInString(text[i:])
			if r == '\u2028' || r == '\u2029' {
				n = 6
			} else if r == utf8.RuneError && size == 1 {
				n = 6
			} else {
				n = size
			}
			i += size - 1
		}
		if err := s.add(n); err != nil {
			return err
		}
		i++
	}
	return nil
}
func (s *jsonSizer) container(n, depth int, item func(int) error) error {
	if err := s.add(2); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if i > 0 {
			if err := s.add(1); err != nil {
				return err
			}
		}
		if err := s.line(depth + 1); err != nil {
			return err
		}
		if err := item(i); err != nil {
			return err
		}
	}
	if n > 0 {
		return s.line(depth)
	}
	return nil
}
func (s *jsonSizer) value(v reflect.Value, depth int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if !v.IsValid() {
		return s.add(4)
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return s.add(4)
		}
		return s.value(v.Elem(), depth)
	}
	if v.Type() == reflect.TypeFor[json.RawMessage]() {
		if v.IsNil() {
			return s.add(4)
		}
		return s.raw(v.Bytes(), depth)
	}
	if v.Type() == reflect.TypeFor[json.Number]() {
		return s.add(len(v.String()))
	}
	switch v.Kind() {
	case reflect.String:
		return s.str(v.String())
	case reflect.Bool:
		if v.Bool() {
			return s.add(4)
		}
		return s.add(5)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return s.add(len(strconv.FormatInt(v.Int(), 10)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return s.add(len(strconv.FormatUint(v.Uint(), 10)))
	case reflect.Float32, reflect.Float64:
		return s.add(32) // conservative bounded scalar, never source numeric lexemes
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return s.add(4)
		}
		return s.container(v.Len(), depth, func(i int) error { return s.value(v.Index(i), depth+1) })
	case reflect.Map:
		if v.IsNil() {
			return s.add(4)
		}
		keys := v.MapKeys()
		return s.container(len(keys), depth, func(i int) error {
			if err := s.str(keys[i].String()); err != nil {
				return err
			}
			if err := s.colon(); err != nil {
				return err
			}
			return s.value(v.MapIndex(keys[i]), depth+1)
		})
	case reflect.Struct:
		var fields []int
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			if f.PkgPath != "" || tag[0] == "-" {
				continue
			}
			if len(tag) > 1 && tag[1] == "omitempty" && emptyJSON(v.Field(i)) {
				continue
			}
			fields = append(fields, i)
		}
		return s.container(len(fields), depth, func(i int) error {
			n := fields[i]
			f := v.Type().Field(n)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" {
				name = f.Name
			}
			if err := s.str(name); err != nil {
				return err
			}
			if err := s.colon(); err != nil {
				return err
			}
			return s.value(v.Field(n), depth+1)
		})
	default:
		return fmt.Errorf("unsupported output accounting type %s", v.Type())
	}
}
func (s *jsonSizer) colon() error {
	if s.indent {
		return s.add(2)
	}
	return s.add(1)
}
func emptyJSON(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	default:
		return v.IsZero()
	}
}
