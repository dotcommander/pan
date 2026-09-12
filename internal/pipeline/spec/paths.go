// Package spec — paths.go handles config-directory resolution and
// bare-name → file path resolution. This is the single source of truth
// for "where do pan pipeline specs live at runtime".
package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FindSourceRoot returns the nearest directory at or above specDir that
// contains a go.mod file. Falls back to specDir when no go.mod is found.
func FindSourceRoot(specDir string) string {
	dir := specDir
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	return specDir
}

// HasGlobMeta reports whether s contains filepath.Glob metacharacters.
// Match set: '*', '?', '['. Escaped '[*]' etc. still counts as meta —
// we rely on filepath.Glob's own escape handling (it returns a single-match
// slice or an empty slice; the caller treats empty match as "missing").
func HasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// resolveFileRef expands a spec file reference against projectRoot.
// Returns (matches, isGlob). For a literal path, matches is the single
// absolute path (no Stat check — caller does that). For a glob, matches
// is the result of filepath.Glob (possibly empty). Malformed glob patterns
// (e.g. unbalanced '[') yield matches=nil, isGlob=true.
func resolveFileRef(f, projectRoot string) (matches []string, isGlob bool) {
	abs := f
	if !filepath.IsAbs(f) {
		abs = filepath.Join(projectRoot, f)
	}
	if !HasGlobMeta(f) {
		return []string{abs}, false
	}
	m, err := filepath.Glob(abs)
	if err != nil {
		// filepath.ErrBadPattern — treat as zero-match glob
		return nil, true
	}
	return m, true
}

// appDirName is the subdirectory under the user config dir that is pan's
// config home, shared with pan's runtime configuration.
const appDirName = "pan"

// specSubdir is the subdirectory under pan's config home where pipeline
// specs live. Bare names (for example `flow render paper`) resolve here.
const specSubdir = "pipelines"

// UserDir returns pan's user-level config home (for example
// $HOME/.config/pan), matching the directory pan's runtime configuration
// uses. Specs live in UserDir()/pipelines — see DataDir.
func UserDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(base, appDirName), nil
}

// DataDir returns the user-level pipeline spec directory (for example
// $HOME/.config/pan/pipelines). Bare-name spec arguments resolve here.
func DataDir() (string, error) {
	ud, err := UserDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(ud, specSubdir), nil
}

// ProjectName extracts a project name from dir by reading go.mod (last
// module-path segment), then package.json ("name" field), then the directory
// basename, and finally falling back to "myproject".
func ProjectName(dir string) string {
	// Try go.mod: "module github.com/owner/NAME"
	if raw, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				mod := strings.TrimPrefix(line, "module ")
				mod = strings.TrimSpace(mod)
				parts := strings.Split(mod, "/")
				if len(parts) > 0 {
					return parts[len(parts)-1]
				}
			}
		}
	}
	// Try package.json: { "name": "..." }
	if raw, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &pkg) == nil && pkg.Name != "" {
			return pkg.Name
		}
	}
	// Fallback: directory basename
	if base := filepath.Base(dir); base != "" && base != "." {
		return base
	}
	return "myproject"
}

// ScanOutputPath returns the default output path for a freshly scanned
// spec: <DataDir>/<project-name>.yaml. It ensures DataDir exists so a
// caller can write the scan result directly.
func ScanOutputPath(dir string) (string, error) {
	dd, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dd, 0o750); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	return filepath.Join(dd, ProjectName(dir)+".yaml"), nil
}

// Resolve turns a user-supplied spec argument into a filesystem path.
// Rules (first match wins):
//  1. If arg contains a path separator OR has a .yaml/.yml extension:
//     return it verbatim (the caller intends a literal path).
//  2. If arg exists as-is in CWD (file or dir): return cleaned arg.
//  3. Otherwise treat arg as a bare name and return <DataDir>/<arg>.yaml.
func Resolve(arg string) (string, error) {
	if arg == "" {
		return "", errors.New("empty spec path")
	}
	if strings.ContainsRune(arg, os.PathSeparator) || strings.ContainsRune(arg, '/') {
		return filepath.Clean(arg), nil
	}
	ext := strings.ToLower(filepath.Ext(arg))
	if ext == ".yaml" || ext == ".yml" {
		return filepath.Clean(arg), nil
	}
	if resolved, ok := resolveExisting(arg); ok {
		return resolved, nil
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, arg+".yaml"), nil
}

// resolveExisting maps an argument that exists in the working directory to
// its spec path. A non-directory executable binary whose bare name has a
// pipeline spec shadows the binary; every other existing path resolves to
// the cleaned argument. ok is false when arg does not exist in the working
// directory.
func resolveExisting(arg string) (string, bool) {
	fi, err := os.Stat(arg)
	if err != nil {
		return "", false
	}
	if !fi.IsDir() && isExecutableBinary(arg) {
		if specPath, ok := specForBinary(arg); ok {
			return specPath, true
		}
	}
	return filepath.Clean(arg), true
}

// specForBinary returns the pipeline spec named after an executable binary,
// when one exists under the data dir.
func specForBinary(arg string) (string, bool) {
	dir, err := DataDir()
	if err != nil {
		return "", false
	}
	specInDir := filepath.Join(dir, arg+".yaml")
	if _, err := os.Stat(specInDir); err == nil {
		return specInDir, true
	}
	return "", false
}

// isExecutableBinary checks if a file begins with Mach-O, ELF, or Windows PE
// magic headers.
func isExecutableBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	var hdr [4]byte
	n, err := f.Read(hdr[:])
	if err != nil || n < 4 {
		return false
	}
	return isMachOMagic(hdr) || isELFMagic(hdr) || isPEMagic(hdr)
}

// isMachOMagic reports whether hdr is one of the Mach-O magic numbers:
// 32/64-bit in either byte order, plus the fat-binary magic.
func isMachOMagic(hdr [4]byte) bool {
	switch hdr {
	case [4]byte{0xcf, 0xfa, 0xed, 0xfe}:
		return true
	case [4]byte{0xfe, 0xed, 0xfa, 0xcf}:
		return true
	case [4]byte{0xce, 0xfa, 0xed, 0xfe}:
		return true
	case [4]byte{0xfe, 0xed, 0xfa, 0xce}:
		return true
	case [4]byte{0xca, 0xfe, 0xba, 0xbe}:
		return true
	}
	return false
}

// isELFMagic reports whether hdr is the ELF magic \x7fELF.
func isELFMagic(hdr [4]byte) bool {
	return hdr[0] == 0x7f && hdr[1] == 'E' && hdr[2] == 'L' && hdr[3] == 'F'
}

// isPEMagic reports whether hdr starts with the Windows PE MZ magic.
func isPEMagic(hdr [4]byte) bool {
	return hdr[0] == 'M' && hdr[1] == 'Z'
}
