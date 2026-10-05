# Agent protocols

Use this guide to discover Pan's machine interfaces before wiring a coding agent to its standard input and output streams. Run `./pan agent schema` first to obtain the contract; both protocols cap each request line at 1 MiB.

## Read the contract before sending requests

`agent schema` emits the bounded machine-readable description of both protocols.

```bash
./pan agent schema
```

The result describes a sequential JSONL protocol and a JSON-RPC 2.0 service over NDJSON. JSONL means one JSON object per line; NDJSON means newline-delimited JSON.

## Choose an interface

| Need | Command | Framing | What it does |
| --- | --- | --- | --- |
| A sequential review session | `./pan --repo . agent stdio` | One JSON request and one JSON response per line. | Supports `hello`, `scan`, `query`, `context`, `feedback`, and `report`. |
| A JSON-RPC service | `./pan --repo . agent serve` | One JSON-RPC 2.0 request and response per line. | Serves repository-map, symbol, file-context, status, snapshot-freshness, overview, and report methods. |
| The current contract | `./pan agent schema` | Ordinary command output. | Emits the schema for both interfaces. |

All protocol commands above are source-checked and unexecuted in this documentation update.

## Start a JSONL session

**Prerequisite:** reserve standard output for protocol responses. Send one complete JSON request line, then read one response line.

```json
{"schema":"pan.agent/v1","id":"h1","op":"hello"}
```

A successful response carries the same request ID and reports `"ok":true`; the `hello` operation returns the protocol contract. This follows from the sequential JSONL handler, which decodes one request object per line and emits one response per line. Next, send `scan` or `report` with a new ID when the schema identifies the request fields you need.

The protocol is local and does not contact a provider or mutate the target repository. `agent stdio --outcomes PATH` is the explicit exception for local outcome recording: it enables `feedback` writes to the selected ledger.

## Check evidence freshness on the service

`snapshot/status` reports whether the served evidence is ready, when it was built and last verified fresh, how many rebuilds the session performed, and whether analysis coverage was complete. Every serve request re-verifies freshness before answering, so `verified_at` is the moment the status was proven current.

```json
{"jsonrpc":"2.0","id":1,"method":"snapshot/status"}
```

The response's `source` distinguishes cache-loaded from live-built evidence, `snapshot_id` identifies the evidence revision, and `limits`/`skipped_count` carry the coverage caveats before treating absent results as conclusive.

## Limits and failures

One stdio or service request line is limited to 1 MiB. One operation is bounded by a 90-second per-request deadline; exceeding it answers one `request_timeout` error (service: server error with message "request timeout") and the session stays open. The schema documents stable error codes and method parameter shapes; use it instead of assuming a field is accepted. `agent serve` rejects malformed JSON-RPC, unknown methods, and invalid parameters with JSON-RPC error codes.

| Symptom | Cause | Solution | Prevention |
| --- | --- | --- | --- |
| A JSONL request receives `invalid_request` or `unsupported_schema`. | The line is malformed or its schema is not `pan.agent/v1`. | Compare the request with `agent schema`. | Treat the schema output as the protocol owner. |
| A service request returns JSON-RPC `-32601`. | The method is not a supported `pan/*`, `map/*`, `symbol/*`, `file/*`, or `snapshot/*` method. | Select a documented method from the schema. | Do not infer methods from CLI command names. |
| A feedback request cannot write an outcome. | No `--outcomes` path was supplied, or the ledger write failed. | Supply an explicit local outcome path and handle the response. | Keep outcome recording opt-in. |
