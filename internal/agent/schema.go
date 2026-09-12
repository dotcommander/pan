package agent

// SchemaDocumentSchema stamps the machine-readable agent schema document.
const SchemaDocumentSchema = "pan.agent-schema/v1"

// paramsNone marks serve methods that take no parameters.
const paramsNone = "none"

// SchemaDocument is the bounded, deterministic contract for both machine
// surfaces: the sequential stdio protocol and the JSON-RPC serve loop.
// Building it inspects no repository and reads no configuration.
type SchemaDocument struct {
	Schema string      `json:"schema"`
	Stdio  StdioSchema `json:"stdio"`
	Serve  ServeSchema `json:"serve"`
}

// StdioSchema describes the sequential pan.agent/v1 protocol.
type StdioSchema struct {
	Schema          string          `json:"schema"`
	Framing         string          `json:"framing"`
	Operations      []OperationSpec `json:"operations"`
	MaxRequestBytes int             `json:"max_request_bytes"`
	MaxContextBytes int             `json:"max_context_bytes"`
	ErrorCodes      []ErrorCodeSpec `json:"error_codes"`
}

// OperationSpec describes one stdio operation.
type OperationSpec struct {
	Name     string `json:"name"`
	Summary  string `json:"summary"`
	Request  string `json:"request"`
	Response string `json:"response"`
}

// ErrorCodeSpec describes one stable stdio error code.
type ErrorCodeSpec struct {
	Code    string `json:"code"`
	Meaning string `json:"meaning"`
}

// ServeSchema describes the JSON-RPC 2.0 NDJSON service.
type ServeSchema struct {
	JSONRPC         string            `json:"jsonrpc"`
	Framing         string            `json:"framing"`
	Methods         []MethodSpec      `json:"methods"`
	MaxRequestBytes int               `json:"max_request_bytes"`
	ErrorCodes      []NumericCodeSpec `json:"error_codes"`
}

// MethodSpec describes one serve method.
type MethodSpec struct {
	Method  string `json:"method"`
	Summary string `json:"summary"`
	Params  string `json:"params"`
	Result  string `json:"result"`
}

// NumericCodeSpec describes one JSON-RPC error code used by serve.
type NumericCodeSpec struct {
	Code    int    `json:"code"`
	Name    string `json:"name"`
	Meaning string `json:"meaning"`
}

// BuildSchema returns the complete agent schema document.
func BuildSchema() SchemaDocument {
	return SchemaDocument{Schema: SchemaDocumentSchema, Stdio: panStdioSchema(), Serve: agentServeSchema()}
}

func panStdioSchema() StdioSchema {
	return StdioSchema{Schema: Schema, Framing: "one JSON request object per line, one JSON response object per line", Operations: []OperationSpec{
		{Name: OpHello, Summary: "Return the protocol contract: schema, operations, caps, error model.", Request: `{"schema":"pan.agent/v1","id":"...","op":"hello"}`, Response: `{"schema","id","ok":true,"result":{"protocol","schema_version","report_schema","operations","max_request_bytes","scoring"}}`},
		{Name: OpScan, Summary: "Return the review document's deterministic read queue and cull ledger.", Request: `{"schema":"pan.agent/v1","id":"...","op":"scan"}`, Response: `{"schema","id","ok":true,"result":{"schema":"pan.review-report/v1","report_id","read_queue","cull_ledger"}}`},
		{Name: OpQuery, Summary: "Select bounded review rows by evidence id or case-insensitive focus.", Request: `{"schema":"pan.agent/v1","id":"...","op":"query","target_ids":["optional"],"focus":"optional","limit":50}`, Response: `{"schema","id","ok":true,"result":{"report_id","count","summaries"}}`},
		{Name: OpContext, Summary: "Return selected review-row context for a report snapshot within a requested budget.", Request: `{"schema":"pan.agent/v1","id":"...","op":"context","target_ids":["optional"],"focus":"optional","budget_bytes":1048576}`, Response: `{"schema","id","ok":true,"result":{"schema":"pan.agent-context/v1","report_id","targets"}}`},
		{Name: OpFeedback, Summary: "Append one validated evaluation outcome when --outcomes enabled it.", Request: `{"schema":"pan.agent/v1","id":"...","op":"feedback","report_id","evidence_id","verdict","files_opened","tool_calls","review_ms"}`, Response: `{"schema","id","ok":true,"result":{"recorded":true}}`},
		{Name: OpReport, Summary: "Return one deterministic pan.review-report/v1 review document from the current verified snapshot.", Request: `{"schema":"pan.agent/v1","id":"...","op":"report"}`, Response: `{"schema","id","ok":true,"result":{...pan.review-report/v1 document}}`},
	}, MaxRequestBytes: MaxRequestBytes, MaxContextBytes: MaxRequestBytes, ErrorCodes: agentErrorCodes(errorMeanings{"request schema is not pan.agent/v1", "request op is not a known operation", "a report id or evidence id is absent from the session snapshot", "feedback requires pan agent stdio --outcomes PATH", "the requested outcome ledger could not be appended", "the target changed after the session snapshot was captured", "context budget cannot contain the mandatory packet envelope", "context budget exceeds the protocol byte cap"})}
}

type errorMeanings struct{ schema, operation, target, disabled, write, stale, small, large string }

func agentErrorCodes(m errorMeanings) []ErrorCodeSpec {
	return []ErrorCodeSpec{{Code: CodeInvalidRequest, Meaning: "malformed request line or invalid field set"}, {Code: CodeUnsupportedSchema, Meaning: m.schema}, {Code: CodeUnsupportedOp, Meaning: m.operation}, {Code: CodeRequestTooLarge, Meaning: "request line exceeds the byte cap"}, {Code: CodeScanFailed, Meaning: "the deterministic analysis failed"}, {Code: CodeCanceled, Meaning: "the session context was canceled"}, {Code: CodeTargetNotFound, Meaning: m.target}, {Code: CodeFeedbackDisabled, Meaning: m.disabled}, {Code: CodeFeedbackWrite, Meaning: m.write}, {Code: CodeStaleEvidence, Meaning: m.stale}, {Code: CodeBudgetTooSmall, Meaning: m.small}, {Code: CodeBudgetTooLarge, Meaning: m.large}}
}

func agentServeSchema() ServeSchema {
	return ServeSchema{JSONRPC: ServeJSONRPC, Framing: "NDJSON: one JSON-RPC 2.0 request object per line, one response object per line", Methods: serveMethods(), MaxRequestBytes: MaxRequestBytes, ErrorCodes: []NumericCodeSpec{{Code: ServeCodeParseError, Name: messageParseError, Meaning: "the request line is not one valid JSON object (including oversized lines)"}, {Code: ServeCodeInvalidRequest, Name: "invalid request", Meaning: "wrong jsonrpc version, missing or invalid id or method, or unknown fields"}, {Code: ServeCodeMethodNotFound, Name: "method not found", Meaning: "the method is not a pan/* serve method"}, {Code: ServeCodeInvalidParams, Name: "invalid params", Meaning: "params are not an object or violate method bounds"}, {Code: ServeCodeServerError, Name: "server error", Meaning: "the deterministic analysis failed or the session was canceled"}}}
}
func serveMethods() []MethodSpec {
	return []MethodSpec{{Method: MethodMapRender, Summary: "Render the source-compatible repository map in one requested format.", Params: `{"format":"compact|verbose|detail|lines|xml|structured"}`, Result: `{"content":"rendered map"}`}, {Method: MethodMapStatus, Summary: "Return the current verified repository-map snapshot status.", Params: paramsNone, Result: `{"built_at","stale","root"}`}, {Method: MethodSymbolFind, Summary: "Find source symbols using a bounded query.", Params: `{"query":"required symbol query"}`, Result: `{"matches":[...]}`}, {Method: MethodFileExplain, Summary: "Explain one repository-relative source file.", Params: `{"path":"required repository-relative path"}`, Result: "source explanation object"}, {Method: MethodFileContext, Summary: "Return bounded source context for a symbol query.", Params: `{"query":"required","kind":"optional","file":"optional","max_source_lines":0}`, Result: "source context object"}, {Method: MethodStatus, Summary: "One bounded snapshot's shape for the served repository.", Params: paramsNone, Result: `{"repository","schema","files","symbols","edges","complete","limits"}`}, {Method: MethodOverview, Summary: "The deterministic scan overview for the served repository.", Params: paramsNone, Result: `{"files","symbols","edges","instructions","languages","generated_files","test_files","go_packages"}`}, {Method: MethodReport, Summary: "One deterministic pan.review-report/v1 review document.", Params: paramsNone, Result: "{...pan.review-report/v1 document}"}, {Method: MethodSymbols, Summary: "Case-insensitive substring symbol search over the served repository.", Params: `{"query":"optional substring","top":50}`, Result: `{"total","symbols":[...],"truncations"}`}}
}
