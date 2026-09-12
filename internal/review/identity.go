package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Identity prefixes and domains for content-addressed report evidence.
// Identities are deterministic functions of repo-relative content, so the
// same repository state produces the same identities on every machine.
const (
	identityPrefix   = "sha256:"
	evidenceDomain   = "pan.evidence/v1"
	reportDomain     = "pan.report/v1"
	identityHexLen   = 64
	identityTotalLen = len(identityPrefix) + identityHexLen
)

// sha256Identity derives a stable "sha256:<hex>" identity for one domain and
// JSON-serializable value. The pan types fed here are plain data; a marshal
// failure is a build defect.
func sha256Identity(domain string, value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("review: marshal %s identity: %v", domain, err))
	}
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write(payload)
	return identityPrefix + hex.EncodeToString(h.Sum(nil))
}

// EvidenceIdentity derives the content identity for one read-queue row. It
// covers the path, deterministic score, and recorded reasons, so any change
// to the evidence behind a row produces a new identity.
func EvidenceIdentity(item ReadItem) string {
	return sha256Identity(evidenceDomain, struct {
		Path  string   `json:"path"`
		Score int      `json:"score"`
		Why   []string `json:"why"`
	}{item.Path, item.Score, item.Why})
}

// reportIdentity derives the document identity from the schema and the row
// identities with their scores. Rank is implied by row order, so reordering
// or rescoring changes the identity while pure presentation edits do not.
func reportIdentity(doc Document) string {
	type reportRow struct {
		EvidenceID string `json:"evidence_id"`
		Score      int    `json:"score"`
	}
	rows := make([]reportRow, len(doc.ReadQueue))
	for i, item := range doc.ReadQueue {
		rows[i] = reportRow{item.EvidenceID, item.Score}
	}
	return sha256Identity(reportDomain, struct {
		Schema string      `json:"schema"`
		Rows   []reportRow `json:"rows"`
	}{doc.Schema, rows})
}

// ValidIdentity reports whether value is a well-formed "sha256:<64 lowercase
// hex>" identity as emitted by EvidenceIdentity and reportIdentity.
func ValidIdentity(value string) bool {
	if len(value) != identityTotalLen || value[:len(identityPrefix)] != identityPrefix {
		return false
	}
	for _, b := range value[len(identityPrefix):] {
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}
