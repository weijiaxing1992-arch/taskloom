package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"sync"
	"time"
)

// Successful previews live only in bounded process memory. Each HTTP request
// still reserves quota, checks authority, and rechecks the source/configuration
// before returning any content. Failures never enter this cache.
const aiGenerationPromptVersion = "grounded-preview-v3"

// Omission restores a recent preview; a deliberate regeneration must be an
// explicit boolean and is never part of the content/configuration cache key.
func aiForceNewOption(raw json.RawMessage) (bool, error) {
	if raw == nil {
		return false, nil
	}
	var value *bool
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return false, orgInvalid("重新生成选项必须为布尔值")
	}
	return *value, nil
}

type aiGenerationCacheKey struct {
	db     *sql.DB
	digest [32]byte
}

func (a *App) aiPreviewCacheKey(kind string, settings aiSettings, sourceVersion string, input any) aiGenerationCacheKey {
	return aiGenerationCacheKey{a.db, sha256.Sum256([]byte(jsonText(map[string]any{
		"tenant": tenantID, "project": a.pid(), "user": a.uid(), "kind": kind,
		"promptVersion": aiGenerationPromptVersion, "settingsVersion": settings.Version,
		"model": settings.Model, "baseURL": settings.BaseURL, "credential": settings.Encrypted,
		"sourceVersion": sourceVersion, "input": input,
	})))}
}

type aiGenerationCacheEntry struct {
	value   string
	expires time.Time
}

type aiGenerationCache struct {
	mu         sync.Mutex
	entries    map[aiGenerationCacheKey]aiGenerationCacheEntry
	bytes      int
	maxEntries int
	maxBytes   int
	ttl        time.Duration
	now        func() time.Time
}

var aiPreviews = newAIGenerationCache(64, 4<<20, 5*time.Minute)

func newAIGenerationCache(maxEntries, maxBytes int, ttl time.Duration) *aiGenerationCache {
	return &aiGenerationCache{entries: make(map[aiGenerationCacheKey]aiGenerationCacheEntry), maxEntries: maxEntries, maxBytes: maxBytes, ttl: ttl, now: time.Now}
}

func (c *aiGenerationCache) remove(key aiGenerationCacheKey) {
	c.bytes -= len(c.entries[key].value)
	delete(c.entries, key)
}

func (c *aiGenerationCache) prune(now time.Time) {
	for key, entry := range c.entries {
		if !now.Before(entry.expires) {
			c.remove(key)
		}
	}
}

func (c *aiGenerationCache) get(key aiGenerationCacheKey) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(c.now())
	entry, ok := c.entries[key]
	return entry.value, ok
}

func (c *aiGenerationCache) put(key aiGenerationCacheKey, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.prune(now)
	if value == "" || len(value) > c.maxBytes || c.maxEntries < 1 || c.ttl <= 0 {
		return
	}
	if _, exists := c.entries[key]; exists {
		c.remove(key)
	}
	for len(c.entries) >= c.maxEntries || c.bytes+len(value) > c.maxBytes {
		var oldestKey aiGenerationCacheKey
		var oldest time.Time
		for k, entry := range c.entries {
			if oldest.IsZero() || entry.expires.Before(oldest) {
				oldestKey, oldest = k, entry.expires
			}
		}
		c.remove(oldestKey)
	}
	c.entries[key] = aiGenerationCacheEntry{value: value, expires: now.Add(c.ttl)}
	c.bytes += len(value)
}
