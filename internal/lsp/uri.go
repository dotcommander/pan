package lsp

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// uriSchemeFile is the file URI scheme language servers exchange.
const uriSchemeFile = "file"

// PathToURI encodes one filesystem path as the file:// URI form language
// servers exchange. Relative paths are resolved against the working
// directory first; a path that cannot be resolved is encoded as-is so the
// server, not pan, rejects it.
func PathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if runtime.GOOS == "windows" {
		abs = "/" + filepath.ToSlash(abs)
	}
	return (&url.URL{Scheme: uriSchemeFile, Path: abs}).String()
}

// URIToPath decodes one file:// URI into a filesystem path, resolving any
// percent-encoding. URIs that are not parseable file URIs are returned
// unchanged so callers can surface them verbatim instead of guessing.
func URIToPath(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != uriSchemeFile {
		return uri
	}
	path := parsed.Path
	if runtime.GOOS == "windows" {
		path = filepath.FromSlash(strings.TrimPrefix(path, "/"))
	}
	return path
}

// isSymbolInformationArray reports whether one raw documentSymbol response
// uses the flat SymbolInformation shape (entries carry "location") instead
// of the hierarchical DocumentSymbol shape (entries carry
// "selectionRange"). Go's JSON decoder accepts either shape into either
// type, so the raw bytes are probed before decoding.
func isSymbolInformationArray(raw json.RawMessage) bool {
	if len(raw) == 0 || raw[0] != '[' {
		return false
	}
	return strings.Contains(string(raw), `"location"`)
}
