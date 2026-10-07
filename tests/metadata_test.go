package modules

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTMDbEpisodeLookupAndDiskCache(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("api_key") != "testkey" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/search/tv":
			fmt.Fprint(w, `{"results":[{"id":42,"name":"Example Show","original_name":"Example Show","first_air_date":"2024-01-01"}]}`)
		case "/tv/42":
			fmt.Fprint(w, `{"id":42,"name":"Example Show","first_air_date":"2024-01-01","poster_path":"/poster.jpg","genres":[{"name":"Drama"}]}`)
		case "/tv/42/season/2/episode/5":
			fmt.Fprint(w, `{"season_number":2,"episode_number":5,"name":"The Fifth","still_path":"/still.jpg"}`)
		case "/tv/42/season/2":
			fmt.Fprint(w, `{"episodes":[{},{},{},{},{},{}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := defaults()
	c.APIKey = "testkey"
	c.IndexEnabled = false
	c.PosterFit = "raw"
	p := paths{config: t.TempDir()}
	client := newTMDB(p, c)
	client.base = server.URL
	parsed := parsedMedia{Title: "Example Show", Year: "2024", TV: true, Season: 2, Episode: 5}
	hit, err := client.lookup(context.Background(), parsed)
	if err != nil || hit == nil || hit.Episode != "05 of 6: The Fifth" || !strings.Contains(hit.Poster, "still.jpg") {
		t.Fatal(hit, err)
	}
	before := requests
	again := newTMDB(p, c)
	again.base = server.URL
	hit, err = again.lookup(context.Background(), parsed)
	if err != nil || hit == nil || requests != before {
		t.Fatal("disk cache missed", err)
	}
	if err = clearMetadataCache(p.cacheFolder(c)); err != nil {
		t.Fatal(err)
	}
}
func TestTMDbNoMatchRateLimitCancellationAndSecretErrors(t *testing.T) {
	mode := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(mode)
		if mode == 200 {
			fmt.Fprint(w, `{"results":[]}`)
		}
	}))
	defer server.Close()
	c := defaults()
	c.APIKey = "sensitivekey"
	c.PosterFit = "raw"
	c.IndexEnabled = false
	client := newTMDB(paths{config: t.TempDir()}, c)
	client.base = server.URL
	hit, err := client.search(context.Background(), parsedMedia{Title: "No Match"})
	if err != nil || hit != nil {
		t.Fatal(hit, err)
	}
	mode = 429
	client.requests = map[string]map[string]any{}
	_, err = client.search(context.Background(), parsedMedia{Title: "Retry"})
	if err == nil || strings.Contains(err.Error(), c.APIKey) {
		t.Fatal("rate-limit/secret handling")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.search(ctx, parsedMedia{Title: "Cancelled"})
	if err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestTMDbIndexBatchClosesBeforeRequestsAndFallsBack(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint("corrupt=", corrupt), func(t *testing.T) {
			dir := syntheticIndex(t)
			m, err := readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			if corrupt {
				if err := os.WriteFile(filepath.Join(dir, "current.json"), []byte("invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			firstRequest := true
			var details []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if firstRequest {
					firstRequest = false
					// These deletions also verify prompt closure on Windows, where open
					// file handles can prevent index cleanup. Subsequent candidates must
					// already be collected despite the files disappearing here.
					for _, kind := range []string{"movie", "tv"} {
						for _, ext := range []string{".rows", ".offsets"} {
							if err := os.Remove(filepath.Join(dir, m.Generation+"."+kind+ext)); err != nil {
								t.Errorf("index held during request: %v", err)
							}
						}
					}
				}
				if strings.HasPrefix(r.URL.Path, "/search/") {
					fmt.Fprint(w, `{"results":[]}`)
					return
				}
				details = append(details, r.URL.Path)
				w.WriteHeader(404)
			}))
			defer server.Close()
			c := defaults()
			c.APIKey = "testkey"
			c.IndexEnabled = true
			client := newTMDB(paths{config: t.TempDir()}, c)
			client.index = dir
			client.base = server.URL
			hit, err := client.search(context.Background(), parsedMedia{Title: "Alpha"})
			if err != nil || hit != nil {
				t.Fatal(hit, err)
			}
			expected := []string{"/movie/2", "/movie/3", "/tv/2", "/tv/3"}
			if corrupt {
				expected = nil
			}
			if !reflect.DeepEqual(details, expected) {
				t.Fatal("candidate collection/fallback", details)
			}
		})
	}
}
