package config

import (
	"fmt"
	"os"
)

// LoadReadOnly reads configuration without seeding a missing user file.
// Diagnostic commands use this path to preserve the state they inspect.
func LoadReadOnly() (Config, error) {
	filename, err := userConfigPath()
	if err != nil {
		return Config{}, err
	}
	return loadReadOnlyAt(filename)
}

func loadReadOnlyAt(filename string) (Config, error) {
	data, err := os.ReadFile(filename)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return Decode(data)
}
