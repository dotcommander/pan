root := justfile_directory()
gobin := `go env GOBIN`
bin_dir := if gobin == "" { `go env GOPATH` / "bin" } else { gobin }

# Show available commands.
default:
    @just --list

# Build the repository-local binary.
build:
    go build -o pan ./cmd/pan

# Run focused tests, e.g. just test ./internal/cli.
test package:
    go test {{quote(package)}}

# Run the full repository suite only when explicitly requested.
test-all:
    go test ./...

# Vet a selected package, e.g. just vet ./internal/cli.
vet package:
    go vet {{quote(package)}}

# Vet all packages across the repository.
vet-all:
    go vet ./...

# Format Go source files.
fmt:
    gofmt -w cmd internal tools

# Check Go formatting without modifying files.
fmt-check:
    @test -z "$(gofmt -l cmd internal tools)" || (echo "Unformatted files found:\n$$(gofmt -l cmd internal tools)" && exit 1)

# Clean build artifacts.
clean:
    rm -f pan

# Run full QA pipeline (format check, vet all, full test suite, build).
qa: fmt-check vet-all test-all build
    @echo "✓ All QA checks passed!"

# Dry-run or publish a resumable source and Homebrew release.
release version *ARGS:
    go run ./tools/release {{quote(version)}} {{ARGS}}

# Build and link Pan into GOBIN, or GOPATH/bin; preserve other destinations.
install: build
    #!/bin/sh
    set -eu
    target={{quote(root / "pan")}}
    destination={{quote(bin_dir / "pan")}}
    mkdir -p {{quote(bin_dir)}}
    if [ -L "$destination" ] && [ "$(readlink "$destination")" = "$target" ]; then
        exit 0
    fi
    ln -s "$target" "$destination"
