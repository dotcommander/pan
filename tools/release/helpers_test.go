package main

import (
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"0.1.0", "0.1.0"},
		{"v0.1.0", "0.1.0"},
		{"VERSION=0.2.1", "0.2.1"},
		{"  v1.0.0  ", "1.0.0"},
	} {
		got, err := normalizeVersion(tc.input)
		if err != nil {
			t.Fatalf("normalizeVersion(%q) error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions("0.1.0", "0.1.0") != 0 {
		t.Error("expected 0.1.0 == 0.1.0")
	}
	if compareVersions("0.2.0", "0.1.9") <= 0 {
		t.Error("expected 0.2.0 > 0.1.9")
	}
	if compareVersions("0.1.0", "0.2.0") >= 0 {
		t.Error("expected 0.1.0 < 0.2.0")
	}
}

func TestBinaryVersionFromContent(t *testing.T) {
	content := []byte(`
package cli

var binaryVersion = "v0.1.0"
`)
	v, err := binaryVersionFromContent(content)
	if err != nil {
		t.Fatal(err)
	}
	if v != "0.1.0" {
		t.Errorf("got %q, want 0.1.0", v)
	}

	updated, err := replaceBinaryVersion(content, "0.1.0", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := binaryVersionFromContent(updated)
	if err != nil {
		t.Fatal(err)
	}
	if v2 != "0.2.0" {
		t.Errorf("got %q, want 0.2.0", v2)
	}
}

func TestUpdateFormula(t *testing.T) {
	formula := []byte(`class Pan < Formula
  desc "Repository evidence and guarded improvement workflows for coding agents"
  homepage "https://github.com/dotcommander/pan"
  url "https://github.com/dotcommander/pan/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "oldsha"
  license "MIT"
end
`)
	updated, err := updateFormula(formula, "https://github.com/dotcommander/pan/archive/refs/tags/v0.2.0.tar.gz", "newsha")
	if err != nil {
		t.Fatal(err)
	}
	want := `class Pan < Formula
  desc "Repository evidence and guarded improvement workflows for coding agents"
  homepage "https://github.com/dotcommander/pan"
  url "https://github.com/dotcommander/pan/archive/refs/tags/v0.2.0.tar.gz"
  sha256 "newsha"
  license "MIT"
end
`
	if string(updated) != want {
		t.Errorf("got:\n%s\nwant:\n%s", string(updated), want)
	}
}
