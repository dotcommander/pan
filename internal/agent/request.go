package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type request struct {
	Schema, ID, Op string
	Fields         map[string]json.RawMessage
}

func readRecord(reader *bufio.Reader) (record []byte, oversized bool, err error) {
	for {
		fragment, readErr := reader.ReadSlice('\n')
		appended := appendRecordFragment(record, oversized, fragment)
		record, oversized = appended.record, appended.oversized
		if appended.terminated {
			return record, oversized, nil
		}
		if result, done := recordReadResult(record, oversized, readErr); done {
			return result.record, result.oversized, result.err
		}
	}
}

type recordResult struct {
	record    []byte
	oversized bool
	err       error
}

type recordFragment struct {
	record                []byte
	oversized, terminated bool
}

func appendRecordFragment(record []byte, oversized bool, fragment []byte) recordFragment {
	if len(fragment) == 0 {
		return recordFragment{record: record, oversized: oversized}
	}
	terminated := fragment[len(fragment)-1] == '\n'
	if terminated {
		fragment = trimLineEnding(fragment)
	}
	if oversized || len(record)+len(fragment) > MaxRequestBytes {
		return recordFragment{record: record, oversized: true, terminated: terminated}
	}
	return recordFragment{record: append(record, fragment...), terminated: terminated}
}

func trimLineEnding(fragment []byte) []byte {
	fragment = fragment[:len(fragment)-1]
	if len(fragment) > 0 && fragment[len(fragment)-1] == '\r' {
		return fragment[:len(fragment)-1]
	}
	return fragment
}

func recordReadResult(record []byte, oversized bool, readErr error) (recordResult, bool) {
	if readErr == nil || errors.Is(readErr, bufio.ErrBufferFull) {
		return recordResult{}, false
	}
	if errors.Is(readErr, io.EOF) {
		return recordResult{record: record, oversized: oversized, err: io.EOF}, true
	}
	return recordResult{err: readErr}, true
}

func decodeRequest(line []byte) (request, string) {
	req := request{Fields: map[string]json.RawMessage{}}
	decoder := json.NewDecoder(bytes.NewReader(line))
	if err := decoder.Decode(&req.Fields); err != nil || req.Fields == nil {
		return req, CodeInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return req, CodeInvalidRequest
	}
	if err := decodeString(req.Fields, "schema", &req.Schema, true); err != nil || !knownSchema(req.Schema) {
		if err == nil {
			return req, CodeUnsupportedSchema
		}
		return req, CodeInvalidRequest
	}
	if err := decodeString(req.Fields, "id", &req.ID, true); err != nil || !validID(req.ID) {
		req.ID = ""
		return req, CodeInvalidRequest
	}
	if err := decodeString(req.Fields, "op", &req.Op, true); err != nil {
		return req, CodeInvalidRequest
	}
	if !knownOp(req.Schema, req.Op) {
		return req, CodeUnsupportedOp
	}
	for name := range req.Fields {
		if !requestFieldAllowed(req.Op, name) {
			return req, CodeInvalidRequest
		}
	}
	if !requestFieldsComplete(req) {
		return req, CodeInvalidRequest
	}
	return req, ""
}

func requestFieldsComplete(req request) bool {
	required := []string(nil)
	switch req.Op {
	case OpQuery:
		required = []string{"limit"}
	case OpContext:
		required = []string{"budget_bytes"}
	case OpFeedback:
		required = []string{reportIDField, evidenceIDField, verdictField, filesOpenedField, toolCallsField, reviewMSField}
	}
	for _, name := range required {
		if _, ok := req.Fields[name]; !ok {
			return false
		}
	}
	return true
}
func requestFieldAllowed(op, name string) bool {
	if name == "schema" || name == "id" || name == "op" {
		return true
	}
	return allowedFields(op)[name]
}

func allowedFields(op string) map[string]bool {
	switch op {
	case OpQuery:
		return map[string]bool{"target_ids": true, "focus": true, "limit": true}
	case OpContext:
		return map[string]bool{"target_ids": true, "focus": true, "budget_bytes": true}
	case OpFeedback:
		return map[string]bool{reportIDField: true, evidenceIDField: true, verdictField: true, filesOpenedField: true, toolCallsField: true, reviewMSField: true}
	default:
		return map[string]bool{}
	}
}

func decodeString(fields map[string]json.RawMessage, name string, destination *string, required bool) error {
	raw, ok := fields[name]
	if !ok {
		if required {
			return errors.New("missing field")
		}
		return nil
	}
	if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, destination) != nil {
		return errors.New("invalid string")
	}
	return nil
}
func knownSchema(schema string) bool { return schema == Schema }

func knownOp(schema, op string) bool {
	return op == OpHello || op == OpReport || op == OpScan || op == OpQuery || op == OpContext || op == OpFeedback
}
func validID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, value := range []byte(id) {
		if value < 0x20 || value > 0x7e {
			return false
		}
	}
	return true
}

func writeError(output io.Writer, schema, id, code string) error {
	return writeResponse(output, Response{Schema: responseSchema(schema), ID: id, OK: false, Error: &Error{Code: code}})
}

func writeResponse(output io.Writer, response Response) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode agent response: %w", err)
	}
	encoded = append(encoded, '\n')
	written, err := output.Write(encoded)
	if err != nil {
		return fmt.Errorf("write agent response: %w", err)
	}
	if written != len(encoded) {
		return fmt.Errorf("write agent response: %w", io.ErrShortWrite)
	}
	return nil
}
