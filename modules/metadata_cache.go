package modules

import (
	"container/list"
	"context"
	"sync"
	"time"
)

type mediaMatch struct {
	kind     string
	id       int64
	detail   map[string]any // Immutable decoded response shared between episode lookups.
	complete bool
}

const showMatchCacheLimit = 64

type showMatchKey struct {
	base, language, credential, indexFolder, generation string
	media                                               parsedMedia
	indexEnabled                                        bool
}
type showMatchEntry struct {
	key     showMatchKey
	match   *mediaMatch
	expires time.Time
}
type showMatchCache struct {
	mu      sync.Mutex
	entries map[showMatchKey]*list.Element
	lru     list.List
	now     func() time.Time
}

func (c *showMatchCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
func (c *showMatchCache) get(key showMatchKey) *mediaMatch {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if item := c.entries[key]; item != nil {
		entry := item.Value.(showMatchEntry)
		if c.clock().Before(entry.expires) {
			c.lru.MoveToFront(item)
			return entry.match
		}
		delete(c.entries, key)
		c.lru.Remove(item)
	}
	return nil
}
func (c *showMatchCache) put(key showMatchKey, match *mediaMatch) {
	if c == nil || match == nil || !match.complete {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[showMatchKey]*list.Element)
	}
	if old := c.entries[key]; old != nil {
		c.lru.Remove(old)
	}
	c.entries[key] = c.lru.PushFront(showMatchEntry{key, match, c.clock().Add(seasonCacheTTL).Truncate(time.Second)})
	if len(c.entries) > showMatchCacheLimit {
		oldest := c.lru.Back()
		delete(c.entries, oldest.Value.(showMatchEntry).key)
		c.lru.Remove(oldest)
	}
}
func (t *tmdbClient) match(ctx context.Context, p parsedMedia) (*mediaMatch, error) {
	if !p.TV {
		return t.resolveMatch(ctx, p)
	}
	media := p
	media.Season, media.Episode = 0, 0
	key := showMatchKey{base: t.base, language: t.config.Language, credential: t.config.APIKey, media: media, indexEnabled: t.config.IndexEnabled}
	if key.indexEnabled {
		key.indexFolder = t.index
		if m, err := readManifest(t.index); err == nil {
			key.generation = m.Generation
		}
	}
	if match := t.shows.get(key); match != nil {
		return match, nil
	}
	// Do not resurrect an expired match from the per-lookup request memoization.
	t.requests = make(map[string]map[string]any)
	match, err := t.resolveMatch(ctx, p)
	if err == nil && ctx.Err() == nil {
		t.shows.put(key, match)
	}
	return match, err
}

func (t *tmdbClient) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// Keep movie metadata long-lived while episode counts/names can refresh promptly.
// The same expiry is honored by both the disk cache and the running service.
func metadataExpiry(hit *metadataHit, c settings, now time.Time) time.Time {
	ttl := time.Duration(c.CacheDays) * 24 * time.Hour
	if hit == nil {
		ttl = time.Hour
	} else if !hit.Complete {
		ttl = 10 * time.Minute
	}
	expires := now.Add(ttl)
	if hit != nil && hit.RefreshAt != 0 {
		if refresh := time.Unix(hit.RefreshAt, 0); refresh.Before(expires) {
			expires = refresh
		}
	}
	return expires
}

const metadataCacheLimit = 4096
const metadataPruneBatch = 64

type metadataMaintenanceState struct {
	writes int
	last   time.Time
}
type metadataMaintenance struct {
	mu      sync.Mutex
	folders map[string]metadataMaintenanceState
	now     func() time.Time
	prune   func(string)
}

// Scan once on first use, then per 64 writes or hour. The cache may temporarily
// exceed its target by at most one batch during normal single-service writes.
func (m *metadataMaintenance) afterWrite(folder string) {
	if m == nil {
		trimMetadataCache(folder)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	if m.folders == nil {
		m.folders = make(map[string]metadataMaintenanceState)
	}
	state, seen := m.folders[folder]
	state.writes++
	if seen && state.writes < metadataPruneBatch && now.Sub(state.last) < time.Hour {
		m.folders[folder] = state
		return
	}
	if !seen && len(m.folders) >= 16 {
		var oldest string
		var at time.Time
		for path, value := range m.folders {
			if oldest == "" || value.last.Before(at) {
				oldest, at = path, value.last
			}
		}
		delete(m.folders, oldest)
	}
	prune := m.prune
	if prune == nil {
		prune = trimMetadataCache
	}
	prune(folder)
	m.folders[folder] = metadataMaintenanceState{last: now}
}
