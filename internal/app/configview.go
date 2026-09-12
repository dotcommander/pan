package app

import (
	"github.com/dotcommander/pan/internal/config"
)

// ConfigView is the effective-configuration report behind `config print`:
// where the effective values came from and what they are, after default
// normalization. Building it reads no repository and writes nothing.
type ConfigView struct {
	Source string        `json:"source"`
	Path   string        `json:"path"`
	Config config.Config `json:"config"`
}

// EffectiveConfig reports the normalized effective configuration and its
// source: "user" when a readable user configuration file is in effect,
// "defaults" otherwise.
func (s Service) EffectiveConfig() ConfigView {
	source, path := config.State()
	return ConfigView{Source: source, Path: path, Config: s.deps.Config.Normalized()}
}
