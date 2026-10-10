package modules

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type metadataHit struct {
	Title, Subtitle, Episode, Poster, URL, EpisodeURL string
	Complete                                          bool
}
type cacheEntry struct {
	Expires int64        `json:"expires"`
	Hit     *metadataHit `json:"hit"`
}
type tmdbClient struct {
	client       *http.Client
	base         string
	cache, index string
	config       settings
	requests     map[string]map[string]any
	indexResults *indexSearchCache
	seasons      *seasonCache
}

func newTMDB(p paths, c settings) *tmdbClient {
	return &tmdbClient{client: &http.Client{Timeout: 8 * time.Second}, base: "https://api.themoviedb.org/3", cache: p.cacheFolder(c), index: p.indexFolder(c), config: c, requests: map[string]map[string]any{}}
}
func cacheKey(p parsedMedia, c settings) string {
	b, _ := json.Marshal([]any{p, c.Language, c.EpisodeLookup, c.PosterFit, c.CacheDays, c.IndexEnabled})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (t *tmdbClient) get(ctx context.Context, path string, query url.Values) (map[string]any, error) {
	key := path + "?" + query.Encode()
	if v := t.requests[key]; v != nil {
		return v, nil
	}
	query.Set("language", t.config.Language)
	if len(t.config.APIKey) <= 40 {
		query.Set("api_key", t.config.APIKey)
	}
	request, err := http.NewRequestWithContext(ctx, "GET", t.base+path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, errors.New("Invalid metadata request")
	}
	if len(t.config.APIKey) > 40 {
		request.Header.Set("Authorization", "Bearer "+t.config.APIKey)
	}
	response, err := t.client.Do(request)
	if err != nil {
		return nil, errors.New("TMDb request failed")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 404:
		return nil, nil
	case 429:
		return nil, errors.New("TMDb rate limit; will retry")
	case 401, 403:
		return nil, errors.New("TMDb key rejected")
	case 200:
	default:
		return nil, errors.New("TMDb unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(body) > 2*1024*1024 {
		return nil, errors.New("TMDb response too large")
	}
	v := map[string]any{}
	if json.Unmarshal(body, &v) != nil {
		return nil, errors.New("Invalid TMDb JSON")
	}
	if len(t.requests) >= 64 {
		t.requests = map[string]map[string]any{}
	}
	t.requests[key] = v
	return v, nil
}
func dateYear(s string) string {
	if len(s) >= 4 {
		return s[:4]
	}
	return ""
}
func recordTitle(r map[string]any) string { return first(text(r, "title"), text(r, "name")) }
func candidateScore(r map[string]any, kind string, qs []string, year string) (float64, float64) {
	likeness := 0.
	for _, q := range qs {
		likeness = max(likeness, similarity(q, recordTitle(r)), similarity(q, first(text(r, "original_title"), text(r, "original_name"))))
	}
	score := likeness * 100
	actual := dateYear(first(text(r, "release_date"), text(r, "first_air_date")))
	if year != "" && actual != "" {
		wanted, _ := strconv.Atoi(year)
		got, _ := strconv.Atoi(actual)
		difference := math.Abs(float64(wanted - got))
		if difference == 0 {
			score += 18
		} else if difference == 1 {
			score += 2
		} else {
			score -= 18
		}
	}
	return score, likeness
}
func imageURL(path, size string) string {
	if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.ContainsAny(path, "?\r\n") {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + path
}
func (t *tmdbClient) artwork(ctx context.Context, raw, backdrop string) (string, bool) {
	if raw == "" {
		return backdrop, true
	}
	if t.config.PosterFit == "raw" {
		return raw, true
	}
	fitted := "https://wsrv.nl/?url=" + url.QueryEscape(raw) + "&w=512&h=512&fit=contain&bg=000000"
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "HEAD", fitted, nil)
	r, err := t.client.Do(req)
	if err != nil {
		return raw, false
	}
	r.Body.Close()
	if r.StatusCode >= 200 && r.StatusCode < 300 && strings.HasPrefix(r.Header.Get("Content-Type"), "image/") {
		return fitted, true
	}
	return raw, false
}
func (t *tmdbClient) lookup(ctx context.Context, p parsedMedia) (*metadataHit, error) {
	if t.config.APIKey == "" {
		return nil, nil
	}
	key := cacheKey(p, t.config)
	file := filepath.Join(t.cache, key+".json")
	var cached cacheEntry
	if readJSON(file, 65536, &cached) == nil && cached.Expires > time.Now().Unix() {
		return cached.Hit, nil
	}
	hit, err := t.search(ctx, p)
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(t.config.CacheDays) * 24 * time.Hour
	if hit == nil {
		ttl = time.Hour
	} else if !hit.Complete {
		ttl = 10 * time.Minute
	}
	body, _ := json.Marshal(cacheEntry{time.Now().Add(ttl).Unix(), hit})
	if atomicWrite(file, body, 0600) == nil {
		trimMetadataCache(t.cache)
	}
	return hit, nil
}
func (t *tmdbClient) search(ctx context.Context, p parsedMedia) (*metadataHit, error) {
	qs := queries(p)
	if len(qs) == 0 {
		return nil, nil
	}
	type candidate struct {
		kind            string
		record          map[string]any
		score, likeness float64
	}
	entries := []candidate{}
	seen := map[string]bool{}
	kinds := []string{"movie", "tv"}
	if p.TV {
		kinds = []string{"tv"}
	}
	add := func(kind string, r map[string]any) {
		id := int64(number(r, "id"))
		if id <= 0 {
			return
		}
		key := kind + fmt.Sprint(id)
		if seen[key] {
			return
		}
		seen[key] = true
		score, likeness := candidateScore(r, kind, qs, p.Year)
		entries = append(entries, candidate{kind, r, score, likeness})
	}
	var indexMatches map[string][][]int64
	if t.config.IndexEnabled {
		indexMatches = t.indexResults.batch(t.index, qs, kinds)
	}
	for _, kind := range kinds {
		for queryIndex, q := range qs {
			if matches := indexMatches[kind]; matches != nil {
				for _, id := range matches[queryIndex] {
					r, e := t.get(ctx, "/"+kind+"/"+fmt.Sprint(id), url.Values{})
					if e != nil {
						return nil, e
					}
					if r != nil {
						add(kind, r)
					}
				}
			}
			data, e := t.get(ctx, "/search/"+kind, url.Values{"query": {q}, "include_adult": {"false"}})
			if e != nil {
				return nil, e
			}
			if data == nil {
				continue
			}
			results, _ := data["results"].([]any)
			for i, v := range results {
				if i >= 20 {
					break
				}
				if r, ok := v.(map[string]any); ok {
					add(kind, r)
				}
			}
		}
	}
	best := -1
	second := -1.
	for i := range entries {
		if entries[i].likeness < 1 && i < 8 {
			entry := &entries[i]
			aliases, e := t.get(ctx, "/"+entry.kind+"/"+fmt.Sprint(int64(number(entry.record, "id")))+"/alternative_titles", url.Values{})
			if e != nil {
				return nil, e
			}
			list, _ := aliases["titles"].([]any)
			if entry.kind == "tv" {
				list, _ = aliases["results"].([]any)
			}
			for _, v := range list {
				r, ok := v.(map[string]any)
				if !ok {
					continue
				}
				for _, q := range qs {
					l := similarity(q, text(r, "title"))
					if l > entry.likeness {
						entry.score += (l - entry.likeness) * 100
						entry.likeness = l
					}
				}
			}
		}
		if best < 0 || entries[i].score > entries[best].score {
			if best >= 0 {
				second = entries[best].score
			}
			best = i
		} else {
			second = max(second, entries[i].score)
		}
	}
	if best < 0 {
		return nil, nil
	}
	selected := entries[best]
	if selected.likeness < .72 || selected.score < 65 || second >= 0 && selected.score-second < 3 {
		return nil, nil
	}
	id := int64(number(selected.record, "id"))
	detail, e := t.get(ctx, "/"+selected.kind+"/"+fmt.Sprint(id), url.Values{})
	if e != nil {
		return nil, e
	}
	complete := detail != nil
	if detail == nil {
		detail = selected.record
	}
	hit := &metadataHit{Title: recordTitle(detail), URL: "https://www.themoviedb.org/" + selected.kind + "/" + fmt.Sprint(id), Complete: complete}
	year := dateYear(first(text(detail, "release_date"), text(detail, "first_air_date")))
	genres := []string{}
	if values, ok := detail["genres"].([]any); ok {
		for _, v := range values {
			if r, ok := v.(map[string]any); ok && text(r, "name") != "" {
				genres = append(genres, text(r, "name"))
				if len(genres) == 2 {
					break
				}
			}
		}
	}
	hit.Subtitle = strings.Trim(strings.Join([]string{year, strings.Join(genres, ", ")}, " · "), " ·")
	raw := imageURL(text(detail, "poster_path"), "w500")
	hit.Poster, complete = t.artwork(ctx, raw, imageURL(text(detail, "backdrop_path"), "w780"))
	hit.Complete = hit.Complete && complete
	if selected.kind == "tv" && p.TV {
		hit.Episode = fmt.Sprintf("%02d", p.Episode)
		if t.config.EpisodeLookup {
			season, e := t.season(ctx, id, p.Season)
			if e != nil {
				return nil, e
			}
			var episode map[string]any
			if season != nil {
				for _, record := range season.episodes {
					if record.number == p.Episode && record.name != "" {
						episode = map[string]any{"season_number": float64(p.Season), "episode_number": float64(record.number), "name": record.name, "still_path": record.still}
						break
					}
				}
			} else {
				hit.Complete = false
			}
			// Some season responses omit usable records. Preserve exact-episode fallback.
			if episode == nil {
				episode, e = t.get(ctx, fmt.Sprintf("/tv/%d/season/%d/episode/%d", id, p.Season, p.Episode), url.Values{})
				if e != nil {
					return nil, e
				}
			}
			if episode == nil {
				hit.Complete = false
			}
			if episode != nil && int(number(episode, "season_number")) == p.Season && int(number(episode, "episode_number")) == p.Episode {
				if season != nil && season.count >= p.Episode {
					hit.Episode += fmt.Sprintf(" of %d", season.count)
				}
				if name := text(episode, "name"); name != "" {
					hit.Episode += ": " + name
				}
				if still := imageURL(text(episode, "still_path"), "w780"); still != "" {
					hit.Poster = still
				}
				hit.EpisodeURL = fmt.Sprintf("%s/season/%d/episode/%d", hit.URL, p.Season, p.Episode)
			} else {
				hit.Complete = false
			}
		}
	}
	return hit, nil
}

// Cache compact season records for sequential episodes, scoped to credentials,
// language and endpoint. A short TTL allows ongoing seasons to change promptly.
const seasonCacheLimit = 16
const seasonCacheTTL = 15 * time.Minute

type seasonEpisode struct {
	number      int
	name, still string
}
type seasonResponse struct {
	count    int
	episodes []seasonEpisode
}
type seasonKey struct {
	base, language, credential string
	show                       int64
	season                     int
}
type seasonEntry struct {
	key      seasonKey
	response *seasonResponse
	expires  time.Time
}
type seasonCache struct {
	mu      sync.Mutex
	entries map[seasonKey]*list.Element
	lru     list.List
	now     func() time.Time
}

func (c *seasonCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
func (c *seasonCache) get(key seasonKey) *seasonResponse {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if item := c.entries[key]; item != nil {
		entry := item.Value.(seasonEntry)
		if c.clock().Before(entry.expires) {
			c.lru.MoveToFront(item)
			return entry.response
		}
		delete(c.entries, key)
		c.lru.Remove(item)
	}
	return nil
}
func (c *seasonCache) put(key seasonKey, response *seasonResponse) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[seasonKey]*list.Element)
	}
	if old := c.entries[key]; old != nil {
		delete(c.entries, key)
		c.lru.Remove(old)
	}
	c.entries[key] = c.lru.PushFront(seasonEntry{key, response, c.clock().Add(seasonCacheTTL)})
	if len(c.entries) > seasonCacheLimit {
		oldest := c.lru.Back()
		delete(c.entries, oldest.Value.(seasonEntry).key)
		c.lru.Remove(oldest)
	}
}
func (t *tmdbClient) season(ctx context.Context, show int64, season int) (*seasonResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credential := sha256.Sum256([]byte(t.config.APIKey))
	key := seasonKey{t.base, t.config.Language, hex.EncodeToString(credential[:]), show, season}
	if cached := t.seasons.get(key); cached != nil {
		return cached, nil
	}
	path := fmt.Sprintf("/tv/%d/season/%d", show, season)
	// Expired shared records must not be resurrected by the per-lookup request cache.
	delete(t.requests, path+"?")
	data, err := t.get(ctx, path, url.Values{})
	if err != nil || data == nil {
		return nil, err
	}
	records, ok := data["episodes"].([]any)
	if !ok {
		return nil, nil
	}
	response := &seasonResponse{count: len(records)}
	for _, value := range records {
		record, ok := value.(map[string]any)
		if !ok || int(number(record, "season_number")) != season {
			continue
		}
		n := int(number(record, "episode_number"))
		if n < 1 {
			continue
		}
		response.episodes = append(response.episodes, seasonEpisode{n, text(record, "name"), text(record, "still_path")})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Missing/malformed records retain fallback behavior and are not shared.
	if len(response.episodes) > 0 {
		t.seasons.put(key, response)
	}
	return response, nil
}

func enrich(s *Snapshot, hit *metadataHit) {
	if s == nil || hit == nil {
		return
	}
	if hit.Title != "" {
		s.Media["title"] = hit.Title
	}
	s.Media["subtitle"] = hit.Subtitle
	s.Media["episode"] = hit.Episode
	s.Media["poster"] = hit.Poster
	s.Media["url"] = hit.URL
	s.Media["episode_url"] = hit.EpisodeURL
	if strings.Contains(hit.Episode, ": ") {
		s.Playback["chapter_eligible"] = false
	}
}
func clearMetadataCache(folder string) error {
	entries, err := os.ReadDir(folder)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") && len(strings.TrimSuffix(entry.Name(), ".json")) == 64 {
			if err = os.Remove(filepath.Join(folder, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func trimMetadataCache(folder string) {
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) <= 4096 {
		return
	}
	type item struct {
		path     string
		modified time.Time
	}
	files := []item{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") && len(strings.TrimSuffix(entry.Name(), ".json")) == 64 {
			info, e := entry.Info()
			if e == nil {
				files = append(files, item{filepath.Join(folder, entry.Name()), info.ModTime()})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modified.Before(files[j].modified) })
	for i := 0; i < len(files)-4096; i++ {
		_ = os.Remove(files[i].path)
	}
}
