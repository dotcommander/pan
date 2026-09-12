// Package lsp implements pan's bounded language-server integration: local
// detection of configured servers and a minimal synchronous LSP client for
// references, definition, hover, and document symbols. Servers run as local
// subprocesses only; pan never contacts a provider or the network.
package lsp

// Position is one 0-based line/character (UTF-16 code unit) position.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a start/end span in one document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is one document range identified by URI.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// LocationLink is the alternative definition response shape.
type LocationLink struct {
	TargetURI   string `json:"targetUri"`
	TargetRange Range  `json:"targetRange"`
}

// TextDocumentIdentifier identifies a document by URI.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// TextDocumentItem carries full document content for didOpen.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// TextDocumentPositionParams is the base for position-based requests.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// ReferenceParams extends position params with the reference context.
type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
}

// ReferenceContext controls declaration inclusion for references.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// MarkupContent is one hover payload.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// HoverResult is the response to textDocument/hover; it may be null.
type HoverResult struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// DocumentSymbolParams requests document symbols.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DocumentSymbol is one hierarchical document symbol.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// SymbolInformation is the flat symbol format some servers return.
type SymbolInformation struct {
	Name     string     `json:"name"`
	Kind     SymbolKind `json:"kind"`
	Location Location   `json:"location"`
}

// SymbolKind values follow the LSP numbering.
type SymbolKind int

// SymbolKind constants from the LSP specification.
const (
	SymbolKindFile          SymbolKind = 1
	SymbolKindModule        SymbolKind = 2
	SymbolKindNamespace     SymbolKind = 3
	SymbolKindPackage       SymbolKind = 4
	SymbolKindClass         SymbolKind = 5
	SymbolKindMethod        SymbolKind = 6
	SymbolKindProperty      SymbolKind = 7
	SymbolKindField         SymbolKind = 8
	SymbolKindConstructor   SymbolKind = 9
	SymbolKindEnum          SymbolKind = 10
	SymbolKindInterface     SymbolKind = 11
	SymbolKindFunction      SymbolKind = 12
	SymbolKindVariable      SymbolKind = 13
	SymbolKindConstant      SymbolKind = 14
	SymbolKindString        SymbolKind = 15
	SymbolKindNumber        SymbolKind = 16
	SymbolKindBoolean       SymbolKind = 17
	SymbolKindArray         SymbolKind = 18
	SymbolKindObject        SymbolKind = 19
	SymbolKindKey           SymbolKind = 20
	SymbolKindNull          SymbolKind = 21
	SymbolKindEnumMember    SymbolKind = 22
	SymbolKindStruct        SymbolKind = 23
	SymbolKindEvent         SymbolKind = 24
	SymbolKindOperator      SymbolKind = 25
	SymbolKindTypeParameter SymbolKind = 26
)

// symbolKindNames returns the human-readable kind names indexed by their
// LSP SymbolKind values. The array is built from the typed constants on
// each call so the table can never drift from them and no mutable package
// state exists; the copy is 27 string headers.
func symbolKindNames() [27]string {
	var names [27]string
	names[SymbolKindFile] = "file"
	names[SymbolKindModule] = "module"
	names[SymbolKindNamespace] = "namespace"
	names[SymbolKindPackage] = "package"
	names[SymbolKindClass] = "class"
	names[SymbolKindMethod] = "method"
	names[SymbolKindProperty] = "property"
	names[SymbolKindField] = "field"
	names[SymbolKindConstructor] = "constructor"
	names[SymbolKindEnum] = "enum"
	names[SymbolKindInterface] = "interface"
	names[SymbolKindFunction] = "function"
	names[SymbolKindVariable] = "variable"
	names[SymbolKindConstant] = "constant"
	names[SymbolKindString] = "string"
	names[SymbolKindNumber] = "number"
	names[SymbolKindBoolean] = "boolean"
	names[SymbolKindArray] = "array"
	names[SymbolKindObject] = "object"
	names[SymbolKindKey] = "key"
	names[SymbolKindNull] = "null"
	names[SymbolKindEnumMember] = "enum member"
	names[SymbolKindStruct] = "struct"
	names[SymbolKindEvent] = "event"
	names[SymbolKindOperator] = "operator"
	names[SymbolKindTypeParameter] = "type parameter"
	return names
}

// String returns the human-readable symbol kind name.
func (k SymbolKind) String() string {
	names := symbolKindNames()
	if k > 0 && int(k) < len(names) && names[k] != "" {
		return names[k]
	}
	return "unknown"
}

// DidOpenTextDocumentParams wraps one didOpen payload.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// InitializeParams is the initialize request payload.
type InitializeParams struct {
	ProcessID    int                `json:"processId,omitempty"`
	RootURI      string             `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

// ClientCapabilities declares the subset of LSP pan uses.
type ClientCapabilities struct {
	TextDocument struct {
		Hover struct {
			DynamicRegistration bool `json:"dynamicRegistration"`
		} `json:"hover"`
		Definition struct {
			DynamicRegistration bool `json:"dynamicRegistration"`
		} `json:"definition"`
		References struct {
			DynamicRegistration bool `json:"dynamicRegistration"`
		} `json:"references"`
		DocumentSymbol struct {
			DynamicRegistration               bool `json:"dynamicRegistration"`
			HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport"`
		} `json:"documentSymbol"`
	} `json:"textDocument"`
}
