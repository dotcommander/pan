// Package spec defines the Pan YAML spec types and loader.
package spec

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads and parses a spec file at path.
func Load(path string) (*Spec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read spec %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return LoadReader(path, f)
}

// LoadReader parses a spec from r while retaining sourceName in diagnostics.
// It is the stdin-capable counterpart to Load.
func LoadReader(sourceName string, r io.Reader) (*Spec, error) {
	var s Spec
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(&s); err != nil {
		return nil, fmt.Errorf("parse spec %s: %w", sourceName, err)
	}
	if s.Loop != nil {
		s.populateLoopScopes()
	}
	return &s, nil
}

func (s *Spec) populateLoopScopes() {
	if s == nil || s.Loop == nil {
		return
	}
	for i := range s.Phases {
		if s.Phases[i].Scope == "" {
			if scope := s.LoopPhaseScope(s.Phases[i].Name); scope != PhaseScopeUnknown {
				s.Phases[i].Scope = scope
			}
		}
	}
}

// MarshalYAML serialises a Stage back to YAML using the same tagged-union
// convention that UnmarshalYAML reads:
//   - Fork     → { fork: {...} }
//   - Fanout   → { fanout: {...} }
//   - External → { external: {...} }
//   - Chip     → flat mapping (label, subtitle, style, wide)
func (st Stage) MarshalYAML() (any, error) {
	switch {
	case st.Fork != nil:
		return map[string]any{stageKindFork: st.Fork}, nil
	case st.Fanout != nil:
		return map[string]any{stageKindFanout: st.Fanout}, nil
	case st.External != nil:
		return map[string]any{stageKindExternal: st.External}, nil
	default:
		return st.Chip, nil
	}
}

// UnmarshalYAML implements the tagged-union dispatch for stage entries.
// The YAML can be one of:
//   - a Chip mapping (label: ..., style: ...)
//   - { fork: {...} }
//   - { fanout: {...} }
//   - { external: {...} }
//
// defaultExternalKind is the kind stamped on External stages that omit one.
const defaultExternalKind = "subprocess"

// UnmarshalYAML implements the tagged-union dispatch for stage entries.
// The YAML can be one of:
//   - a Chip mapping (label: ..., style: ...)
//   - { fork: {...} }
//   - { fanout: {...} }
//   - { external: {...} }
func (st *Stage) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("stage must be a mapping, got %v at line %d", node.Kind, node.Line)
	}
	discriminators := stageDiscriminatorCount(node)
	if discriminators > 0 && (discriminators != 1 || len(node.Content) != 2) {
		return fmt.Errorf("stage at line %d must contain exactly one stage shape", node.Line)
	}
	handled, err := decodeStageUnion(st, node)
	if err != nil {
		return err
	}
	if handled {
		return nil
	}
	// No discriminator key — decode as a Chip.
	return decodeChipStage(st, node)
}

// stageDiscriminatorCount counts tagged-union discriminator keys
// (fork/fanout/external) among the node's top-level keys.
func stageDiscriminatorCount(node *yaml.Node) int {
	discriminators := 0
	for i := 0; i < len(node.Content); i += 2 {
		switch node.Content[i].Value {
		case stageKindFork, stageKindFanout, stageKindExternal:
			discriminators++
		}
	}
	return discriminators
}

// decodeStageUnion decodes the tagged-union member named by the node's
// discriminator key, if one is present. handled reports whether a
// discriminator was found; the stage is left untouched otherwise.
func decodeStageUnion(st *Stage, node *yaml.Node) (bool, error) {
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		switch key {
		case stageKindFork:
			var f Fork
			if err := decodeKnownFields(val, &f); err != nil {
				return false, fmt.Errorf("stage.fork at line %d: %w", node.Line, err)
			}
			st.Fork = &f
			return true, nil
		case stageKindFanout:
			var f Fanout
			if err := decodeKnownFields(val, &f); err != nil {
				return false, fmt.Errorf("stage.fanout at line %d: %w", node.Line, err)
			}
			st.Fanout = &f
			return true, nil
		case stageKindExternal:
			var e External
			if err := decodeKnownFields(val, &e); err != nil {
				return false, fmt.Errorf("stage.external at line %d: %w", node.Line, err)
			}
			if e.Kind == "" {
				e.Kind = defaultExternalKind
			}
			st.External = &e
			return true, nil
		}
	}
	return false, nil
}

// decodeChipStage decodes the node as a plain chip mapping.
func decodeChipStage(st *Stage, node *yaml.Node) error {
	var c Chip
	if err := decodeKnownFields(node, &c); err != nil {
		return fmt.Errorf("stage.chip at line %d: %w", node.Line, err)
	}
	st.Chip = &c
	return nil
}

func decodeKnownFields(node *yaml.Node, dst any) error {
	raw, err := yaml.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshal YAML node: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	return decoder.Decode(dst)
}
