package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dotcommander/pan/internal/agent"
	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/review"
)

type agentSource struct {
	Path        string
	Content     string
	Fingerprint string
}

func (a *AgentServeState) captureSources(ctx context.Context, snap analyze.Snapshot) error {
	stamps, err := analyze.Stamps(ctx, a.root, a.service.deps.Config)
	if err != nil {
		return err
	}
	sources := make(map[string]agentSource, len(snap.Files))
	redacted := make(map[string]bool)
	hashes := make(map[string]string, len(snap.Files))
	for _, file := range snap.Files {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		path, pathErr := agentSourcePath(a.root, file.Path)
		if pathErr != nil {
			return pathErr
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("capture %s: %w", file.Path, readErr)
		}
		if int64(len(contents)) != file.Size {
			return fmt.Errorf("capture %s: size changed", file.Path)
		}
		hashes[file.Path] = agentContentFingerprint(contents)
		if agentSecretSource(file.Path, contents) {
			redacted[file.Path] = true
			continue
		}
		sources[file.Path] = agentSource{Path: file.Path, Content: string(contents), Fingerprint: agentContentFingerprint(contents)}
	}
	verifiedStamps, err := analyze.Stamps(ctx, a.root, a.service.deps.Config)
	if err != nil {
		return err
	}
	if !agentSameStamps(stamps, verifiedStamps) {
		return errors.New("source changed during session capture")
	}
	a.sources, a.redacted, a.hashes, a.stamps, a.fingerprint = sources, redacted, hashes, stamps, agentSourcesFingerprint(sources)
	return nil
}

// Context capture omits the whole file when source credential evidence is
// present. This also covers JSON keys and Go short declarations.
var agentSecretLiteral = regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|token|password|passwd|pwd|client[_-]?secret|authorization)\b["']?\s*(?::=?|=)\s*\S|\bbearer\s+[a-z0-9._~+/-]+|-----BEGIN (?:RSA |DSA |EC |OPENSSH |PGP )?PRIVATE KEY-----|\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{36,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}_[A-Za-z0-9_]{20,}\b|\bsk-[A-Za-z0-9]{20,}\b|\bxox[baprs]-[A-Za-z0-9-]{20,}\b|\bAKIA[0-9A-Z]{16}\b`)

func agentSecretSource(path string, contents []byte) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.Contains(base, "secret") || strings.Contains(base, "credential") {
		return true
	}
	return agentSecretLiteral.Match(contents)
}

func agentSourcePath(root, relative string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(relative))
	resolved, err := filepath.Rel(root, path)
	if err != nil || resolved == ".." || len(resolved) > 2 && resolved[:3] == ".."+string(filepath.Separator) {
		return "", errors.New("unsafe source path")
	}
	return path, nil
}

func agentContentFingerprint(contents []byte) string {
	digest := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func agentSourcesFingerprint(sources map[string]agentSource) string {
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(sources[path].Fingerprint))
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// AgentContext builds a bounded packet from captured, non-secret source evidence.
func (a *AgentServeState) AgentContext(ctx context.Context, doc review.Document, rows []review.ReadItem, budget int) (agent.ContextPacket, error) {
	if doc.ReportID == "" || budget < 1 {
		return agent.ContextPacket{}, agent.ErrContextBudgetTooSmall
	}
	if budget > agent.MaxRequestBytes {
		return agent.ContextPacket{}, agent.ErrContextBudgetTooLarge
	}
	packet := agent.ContextPacket{Schema: agent.ContextSchema, ReportID: doc.ReportID, BudgetBytes: budget, Fingerprint: a.fingerprint, Targets: []agent.ContextTarget{}, Omissions: []string{}}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return agent.ContextPacket{}, err
		}
		source, ok := a.sources[row.Path]
		if !ok {
			if a.redacted[row.Path] {
				packet.Omissions = append(packet.Omissions, row.Path+": redacted secret source")
				continue
			}
			return agent.ContextPacket{}, agent.ErrStaleEvidence
		}
		candidate := agent.ContextTarget{EvidenceID: row.EvidenceID, Path: row.Path, Fingerprint: source.Fingerprint, Content: source.Content, Why: row.Why}
		packet.Targets = append(packet.Targets, candidate)
		encoded, err := json.Marshal(packet)
		if err != nil || len(encoded) > budget {
			packet.Targets = packet.Targets[:len(packet.Targets)-1]
			packet.Omissions = append(packet.Omissions, row.Path+": omitted by context budget")
		}
	}
	for range 8 {
		encoded, err := json.Marshal(packet)
		if err != nil || len(encoded) > budget {
			return agent.ContextPacket{}, agent.ErrContextBudgetTooSmall
		}
		if packet.UsedBytes == len(encoded) {
			return packet, nil
		}
		packet.UsedBytes = len(encoded)
	}
	return agent.ContextPacket{}, errors.New("context packet size did not converge")
}

// AgentVerify rejects evidence whose captured source has changed.
func (a *AgentServeState) AgentVerify(ctx context.Context, doc review.Document) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if doc.ReportID == "" || a.fingerprint == "" {
		return agent.ErrStaleEvidence
	}
	if !a.sourcesFresh(ctx) {
		return agent.ErrStaleEvidence
	}
	return nil
}

func (a *AgentServeState) sourcesFresh(ctx context.Context) bool {
	stamps, err := analyze.Stamps(ctx, a.root, a.service.deps.Config)
	if err != nil || !agentSameStamps(a.stamps, stamps) {
		return false
	}
	for path, fingerprint := range a.hashes {
		full, err := agentSourcePath(a.root, path)
		if err != nil {
			return false
		}
		contents, err := os.ReadFile(full)
		if err != nil || agentContentFingerprint(contents) != fingerprint {
			return false
		}
	}
	return true
}

func agentSameStamps(left, right map[string]analyze.FileStamp) bool {
	if len(left) != len(right) {
		return false
	}
	for path, stamp := range left {
		if other, ok := right[path]; !ok || other != stamp {
			return false
		}
	}
	return true
}
