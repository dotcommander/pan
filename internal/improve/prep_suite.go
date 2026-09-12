package improve

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunPackage tests the target package as a fast gate for batched prep. The
// final full-suite coverage run remains the authority for settlement.
func (t Toolchain) RunPackage(ctx context.Context, dir, sourceFile string) (TestResult, error) {
	timeout := t.TestTimeout
	if timeout <= 0 {
		timeout = defaultTestTimeout
	}
	pkg, err := prepPackageDirectory(dir, sourceFile)
	if err != nil {
		return TestResult{}, err
	}
	cmd := exec.CommandContext(ctx, cmdGo)
	cmd.Args = []string{cmdGo, goTestSubcommand, "-json", "-count=1", "-timeout", timeout.String(), "."}
	cmd.Dir = pkg
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return TestResult{}, ctx.Err()
	}
	result, events := parseTestStream(bytes.NewReader(output), maxTestOutputBytes)
	result.AllPassed = result.AllPassed && err == nil
	if events == 0 && err != nil {
		result.Failures = append(result.Failures, string(output))
	}
	return result, nil
}

func prepPackageDirectory(root, sourceFile string) (string, error) {
	dir := filepath.Clean(filepath.Dir(sourceFile))
	if dir == parentDir || strings.HasPrefix(dir, parentDir+string(filepath.Separator)) || filepath.IsAbs(dir) {
		return "", fmt.Errorf("target package escapes repository: %q", sourceFile)
	}
	return filepath.Join(root, dir), nil
}
