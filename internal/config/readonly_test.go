package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyDoesNotSeedMissingConfig(t *testing.T) {
	t.Parallel()
	filename := filepath.Join(t.TempDir(), "absent", "config.yaml")
	cfg, err := loadReadOnlyAt(filename)
	if err != nil || cfg.MaxFiles != Default().MaxFiles {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	if _, err := os.Stat(filepath.Dir(filename)); !os.IsNotExist(err) {
		t.Fatalf("read-only load created state: %v", err)
	}
}
