package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type metadataFixtureTransport func(*http.Request) (*http.Response, error)

func (f metadataFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEpisodeArtworkKeepsPosterFallbackAndFailureRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/tv":
			fmt.Fprint(w, `{"results":[{"id":42,"name":"Example Show"}]}`)
		case "/tv/42":
			fmt.Fprint(w, `{"id":42,"name":"Example Show","poster_path":"/poster.jpg"}`)
		case "/tv/42/season/1":
			fmt.Fprint(w, `{"episodes":[{"season_number":1,"episode_number":1,"name":"One"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := testPaths(t)
	for _, fittingWorks := range []bool{true, false} {
		cfg := defaults()
		cfg.APIKey = "fixture-key"
		cfg.IndexEnabled = false
		client := newTMDB(p, cfg)
		client.base = server.URL
		var probes atomic.Int32
		client.client = &http.Client{Transport: metadataFixtureTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "wsrv.nl" {
				probes.Add(1)
				if !fittingWorks {
					return nil, fmt.Errorf("poster temporarily unavailable")
				}
				return (presenceFixtureTransport{server.Client().Transport}).RoundTrip(r)
			}
			return server.Client().Transport.RoundTrip(r)
		})}
		hit, err := client.search(context.Background(), parsedMedia{Title: "Example Show", TV: true, Season: 1, Episode: 1})
		if err != nil || hit == nil || hit.Episode != "01 of 1: One" || probes.Load() != 1 || hit.Complete != fittingWorks {
			t.Fatal("poster fallback changed", hit, err, probes.Load())
		}
		if fittingWorks && !strings.HasPrefix(hit.Poster, "https://wsrv.nl/") {
			t.Fatal("fallback poster was not fitted")
		}
		if !fittingWorks && hit.Poster != "https://image.tmdb.org/t/p/w500/poster.jpg" {
			t.Fatal("raw artwork fallback lost")
		}
	}
}

func TestEpisodeMetadataExpiryReachesDiskAndPreservesMovieTTL(t *testing.T) {
	var requests atomic.Int32
	var count atomic.Int32
	var finished atomic.Bool
	count.Store(2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/search/tv":
			fmt.Fprint(w, `{"results":[{"id":42,"name":"Example Show"}]}`)
		case "/tv/42":
			status := "Returning Series"
			if finished.Load() {
				status = "Ended"
			}
			fmt.Fprintf(w, `{"id":42,"name":"Example Show","status":%q}`, status)
		case "/tv/42/season/1":
			episodes := make([]map[string]any, count.Load())
			for i := range episodes {
				episodes[i] = map[string]any{"season_number": 1, "episode_number": i + 1, "name": fmt.Sprint("Episode ", i+1), "still_path": "/still.jpg"}
			}
			json.NewEncoder(w).Encode(map[string]any{"episodes": episodes})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := testPaths(t)
	cfg := defaults()
	cfg.APIKey, cfg.IndexEnabled, cfg.PosterFit = "fixture-key", false, "raw"
	now := time.Unix(1700000000, 750000000)
	seasons := &seasonCache{now: func() time.Time { return now }}
	shows := &showMatchCache{now: func() time.Time { return now }}
	fetch := func(episode int) *metadataHit {
		client := newTMDB(p, cfg)
		client.base, client.seasons, client.shows, client.now = server.URL, seasons, shows, func() time.Time { return now }
		hit, err := client.lookup(context.Background(), parsedMedia{Title: "Example Show", TV: true, Season: 1, Episode: episode})
		if err != nil || hit == nil || !hit.Complete {
			t.Fatalf("episode metadata: %+v %v", hit, err)
		}
		return hit
	}
	hit := fetch(1)
	originalExpiry := hit.RefreshAt
	before := requests.Load()
	now = now.Add(time.Minute)
	fetch(1)
	if requests.Load() != before {
		t.Fatal("unexpired disk cache missed")
	}
	if fetch(2).RefreshAt != originalExpiry || requests.Load() != before {
		t.Fatal("shared season expiry was extended or network work repeated")
	}
	now = time.Unix(originalExpiry, 0)
	count.Store(3)
	if hit = fetch(1); hit.Episode != "01 of 3: Episode 1" || requests.Load() != before+3 {
		t.Fatal("disk cache hid updated season count", hit, requests.Load())
	}
	if metadataExpiry(hit, cfg, now).Unix() != hit.RefreshAt {
		t.Fatal("service expiry differs from episode expiry")
	}
	movie := &metadataHit{Complete: true}
	if metadataExpiry(movie, cfg, now) != now.Add(time.Duration(cfg.CacheDays)*24*time.Hour) {
		t.Fatal("movie cache lifetime changed")
	}
	partial := &metadataHit{Complete: false, RefreshAt: now.Add(time.Hour).Unix()}
	if metadataExpiry(partial, cfg, now) != now.Add(10*time.Minute) {
		t.Fatal("partial-match retry changed")
	}
	now = now.Add(seasonCacheTTL + time.Second)
	finished.Store(true)
	if ended := fetch(1); ended.RefreshAt != now.Add(time.Duration(cfg.CacheDays)*24*time.Hour).Unix() {
		t.Fatal("finished show lost its long-lived cache", ended)
	}
}

func TestShowMatchCacheScopesExpiryFailureRecoveryAndBounds(t *testing.T) {
	var calls atomic.Int32
	var status atomic.Int32
	status.Store(200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(int(status.Load()))
		if status.Load() != 200 {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/search/tv") {
			fmt.Fprint(w, `{"results":[{"id":42,"name":"Example Show"}]}`)
		} else {
			fmt.Fprint(w, `{"id":42,"name":"Example Show"}`)
		}
	}))
	defer server.Close()
	p := testPaths(t)
	cfg := defaults()
	cfg.APIKey = "fixture-key"
	cfg.IndexEnabled = false
	now := time.Now()
	cache := &showMatchCache{now: func() time.Time { return now }}
	media := parsedMedia{Title: "Example Show", TV: true, Season: 1, Episode: 1}
	fetch := func(base string, c settings, m parsedMedia) error {
		client := newTMDB(p, c)
		client.base, client.shows = base, cache
		match, err := client.match(context.Background(), m)
		if err == nil && match == nil {
			return fmt.Errorf("missing match")
		}
		return err
	}
	if err := fetch(server.URL, cfg, media); err != nil {
		t.Fatal(err)
	}
	media.Episode = 2
	media.Season = 2
	fetch(server.URL, cfg, media)
	if calls.Load() != 2 {
		t.Fatal("episode or season fragmented the show cache")
	}
	changed := cfg
	changed.Language = "fr-FR"
	fetch(server.URL, changed, media)
	changed = cfg
	changed.APIKey = "another-key"
	fetch(server.URL, changed, media)
	changed = cfg
	changed.IndexPath = t.TempDir()
	changed.IndexEnabled = true
	fetch(server.URL, changed, media)
	if calls.Load() != 8 {
		t.Fatal("configuration cache scopes mixed", calls.Load())
	}
	dir := syntheticIndex(t)
	changed.IndexPath = dir
	fetch(server.URL, changed, media)
	m, _ := readManifest(dir)
	newGeneration := "g" + strings.Repeat("b", 32)
	for _, kind := range []string{"movie", "tv"} {
		for _, ext := range []string{".rows", ".offsets"} {
			body, err := os.ReadFile(filepath.Join(dir, m.Generation+"."+kind+ext))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, newGeneration+"."+kind+ext), body, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	m.Generation = newGeneration
	body, _ := json.Marshal(m)
	if err := atomicWrite(filepath.Join(dir, "current.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	fetch(server.URL, changed, media)
	if calls.Load() != 12 {
		t.Fatal("index generation did not invalidate the match", calls.Load())
	}
	now = now.Add(seasonCacheTTL + time.Second)
	fetch(server.URL, cfg, media)
	if calls.Load() != 14 {
		t.Fatal("expired show match reused")
	}
	for _, change := range []func(*parsedMedia){func(p *parsedMedia) { p.Year = "2024" }, func(p *parsedMedia) { p.Directory = "Alternate directory" }, func(p *parsedMedia) { p.Title = "Example.Show" }} {
		changedMedia := media
		change(&changedMedia)
		before := calls.Load()
		if err := fetch(server.URL, cfg, changedMedia); err != nil || calls.Load() <= before {
			t.Fatal("title/year/directory scopes mixed", changedMedia, err)
		}
	}
	beforeEndpoint := calls.Load()
	if err := fetch(server.URL+"/v3", cfg, media); err != nil || calls.Load() != beforeEndpoint+2 {
		t.Fatal("TMDb endpoints shared a match", err)
	}
	bad := cfg
	bad.Language = "failure-scope"
	status.Store(503)
	if err := fetch(server.URL, bad, media); err == nil {
		t.Fatal("outage ignored")
	}
	status.Store(200)
	if err := fetch(server.URL, bad, media); err != nil {
		t.Fatal("failure was cached", err)
	}
	for i := 0; i < showMatchCacheLimit+2; i++ {
		c := cfg
		c.Language = fmt.Sprint("lang-", i)
		if err := fetch(server.URL, c, media); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.entries) != showMatchCacheLimit || cache.lru.Len() != showMatchCacheLimit {
		t.Fatal("show cache exceeded bound")
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := fetch(server.URL, cfg, media); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	client := newTMDB(p, cfg)
	client.base, client.shows = server.URL, cache
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := calls.Load()
	if _, err := client.search(ctx, media); err == nil || calls.Load() != before {
		t.Fatal("cancelled lookup reused a cached show")
	}
}

func TestMetadataMaintenanceBatchesWritesAndBoundsFolders(t *testing.T) {
	now := time.Now()
	prunes := 0
	m := &metadataMaintenance{now: func() time.Time { return now }, prune: func(string) { prunes++ }}
	m.afterWrite("cache")
	for i := 0; i < metadataPruneBatch-1; i++ {
		m.afterWrite("cache")
	}
	if prunes != 1 {
		t.Fatal("directory scanned on every write", prunes)
	}
	m.afterWrite("cache")
	if prunes != 2 {
		t.Fatal("batch did not prune", prunes)
	}
	now = now.Add(time.Hour)
	m.afterWrite("cache")
	if prunes != 3 {
		t.Fatal("maintenance deadline ignored")
	}
	for i := 0; i < 30; i++ {
		m.afterWrite(fmt.Sprint("folder-", i))
	}
	if len(m.folders) != 16 {
		t.Fatal("maintenance scopes are unbounded")
	}
	dir := t.TempDir()
	for i := 0; i < metadataCacheLimit+2; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%064x.json", i)), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(dir, "unrelated.json")
	os.WriteFile(other, []byte(`{}`), 0600)
	trimMetadataCache(dir)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != metadataCacheLimit+1 || !exists(other) {
		t.Fatal("pruning lost unrelated files or retained overflow", len(entries), err)
	}
}
