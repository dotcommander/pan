package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dotcommander/pan/internal/analyze"
	"github.com/dotcommander/pan/internal/pipeline/atomicfile"
)

type modelScoreCache struct {
	sync.Mutex
	entries map[string]cachedModelScore
}

func resolveModelCacheDir(o ModelOptions) (ModelOptions, error) {
	if o.CacheDir != "" {
		return o, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ModelOptions{}, fmt.Errorf("resolve model cache directory: %w", err)
	}
	o.CacheDir = filepath.Join(dir, "pan", "review")
	return o, nil
}

// ContentHashes returns SHA-256 identities for readable snapshot files. The
// cache key changes whenever a candidate's source bytes change.
func ContentHashes(root string, files []analyze.File) map[string]string {
	hashes := make(map[string]string, len(files))
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		hashes[file.Path] = hex.EncodeToString(sum[:])
	}
	return hashes
}

func cacheKey(o ModelOptions, row ReadItem) string {
	contentHash := o.ContentHashes[row.Path]
	data, _ := json.Marshal(struct {
		Model, BaseURL, Path, ContentHash string
		Score                             int
		Why                               []string
	}{o.Model, o.BaseURL, row.Path, contentHash, row.Score, row.Why})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func loadModelScore(o ModelOptions, cache *modelScoreCache, row ReadItem) (cachedModelScore, bool) {
	if o.NoCache {
		return cachedModelScore{}, false
	}
	cache.Lock()
	defer cache.Unlock()
	score, ok := cache.entries[cacheKey(o, row)]
	return score, ok
}
func storeModelScore(o ModelOptions, cache *modelScoreCache, key string, score cachedModelScore) {
	if o.NoCache {
		return
	}
	cache.Lock()
	defer cache.Unlock()
	if len(cache.entries) < modelCacheLimit {
		cache.entries[key] = score
	}
}
func cachePath(o ModelOptions) string { return filepath.Join(o.CacheDir, "scores.json") }
func loadDiskCache(o ModelOptions, cache *modelScoreCache) {
	data, err := os.ReadFile(cachePath(o))
	if err != nil || len(data) > 8<<20 {
		return
	}
	entries := map[string]cachedModelScore{}
	if json.Unmarshal(data, &entries) != nil {
		return
	}
	cache.Lock()
	defer cache.Unlock()
	for key, value := range entries {
		if len(cache.entries) < modelCacheLimit {
			cache.entries[key] = value
		}
	}
}
func persistDiskCache(o ModelOptions, cache *modelScoreCache) {
	cache.Lock()
	data, err := json.Marshal(cache.entries)
	cache.Unlock()
	if err != nil || len(data) > 8<<20 {
		return
	}
	if os.MkdirAll(o.CacheDir, 0o700) != nil {
		return
	}
	_ = atomicfile.Write(cachePath(o), data, 0o600)
}
