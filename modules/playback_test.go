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
	"sync/atomic"
	"testing"
	"time"
)

func TestPlaybackHTTPMetadataPrivacyAndSeek(t *testing.T) {
	uri := "file:///private/Show.S02E05.mkv"
	state := "playing"
	position := 12
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, _ := r.BasicAuth()
		if password != "secret" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/requests/status.json":
			fmt.Fprintf(w, `{"version":"3.0.24","apiversion":3,"state":%q,"time":%d,"length":100,"rate":2,"currentplid":7,"information":{"category":{"meta":{"filename":"Show.S02E05.mkv","title":"Embedded Show"}}}}`, state, position)
		case "/requests/playlist.json":
			fmt.Fprintf(w, `{"children":[{"id":"7","uri":%q}]}`, uri)
		}
	}))
	defer server.Close()
	options := map[string]string{"http-port": strings.Split(server.URL, ":")[2], "http-password": "secret"}
	c := defaults()
	client := &playbackClient{}
	snapshot, parsed, err := client.sample(context.Background(), options, c)
	if err != nil || parsed.Season != 2 || snapshot.Media["title"] != "Embedded Show" || number(snapshot.Playback, "position") != 12 {
		t.Fatal(snapshot, parsed, err)
	}
	state = "paused"
	position = 55
	snapshot, _, err = client.sample(context.Background(), options, c)
	if err != nil || makeActivity(snapshot)["timestamps"] != nil || number(snapshot.Playback, "position") != 55 {
		t.Fatal("pause/seek", err)
	}
	c.Ignored = []string{"/private"}
	snapshot, _, err = client.sample(context.Background(), options, c)
	if snapshot != nil || err != nil {
		t.Fatal("ignored media published")
	}
	c.Ignored = nil
	uri = "https://example.test/stream"
	client.pollPlaylist = time.Time{}
	snapshot, _, err = client.sample(context.Background(), options, c)
	if snapshot != nil || err != nil {
		t.Fatal("network media published")
	}
}
func TestPlaybackDisconnectAndMalformedData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"version":"3.0.24","apiversion":3,"state":"playing","currentplid":1}`)
	}))
	options := map[string]string{"http-port": strings.Split(server.URL, ":")[2]}
	client := &playbackClient{}
	s, _, err := client.sample(context.Background(), options, defaults())
	if s != nil {
		t.Fatal("missing URI published", err)
	}
	server.Close()
	s, _, err = client.sample(context.Background(), options, defaults())
	if s != nil || err == nil {
		t.Fatal("disconnect retained media")
	}
}

func TestPlaybackClockJitterSeekPauseRateAndMedia(t *testing.T) {
	clock := playbackClock{}
	sample := func(at, position, rate, duration float64, state, uri string) *Snapshot {
		s := &Snapshot{Version: 3, Updated: at, Media: map[string]any{"uri": uri, "title": "Movie", "state": state}, Playback: map[string]any{"position": position, "duration": duration, "rate": rate}, Options: map[string]string{}}
		clock.apply(s)
		return s
	}
	s := sample(1000.2, 10, 1, 100, "playing", "file:///one")
	initial := makeActivity(s)["timestamps"]
	for _, v := range []struct{ at, pos float64 }{{1000.7, 10}, {1001.2, 11}, {1001.7, 11}, {1002.2, 12}} {
		if got := makeActivity(sample(v.at, v.pos, 1, 100, "playing", "file:///one"))["timestamps"]; !reflect.DeepEqual(got, initial) {
			t.Fatal("clock jitter", got, initial)
		}
	}
	seek := makeActivity(sample(1003.2, 50, 1, 100, "playing", "file:///one"))["timestamps"]
	if reflect.DeepEqual(seek, initial) {
		t.Fatal("seek did not re-anchor")
	}
	if makeActivity(sample(1004, 50, 1, 100, "paused", "file:///one"))["timestamps"] != nil {
		t.Fatal("paused clock retained")
	}
	resume := makeActivity(sample(1010, 50, 1, 100, "playing", "file:///one"))["timestamps"]
	if reflect.DeepEqual(resume, seek) {
		t.Fatal("resume did not re-anchor")
	}
	faster := makeActivity(sample(1011, 51, 2, 100, "playing", "file:///one"))["timestamps"]
	if reflect.DeepEqual(faster, resume) {
		t.Fatal("rate change missed")
	}
	longer := makeActivity(sample(1011.5, 52, 2, 200, "playing", "file:///one"))["timestamps"]
	if reflect.DeepEqual(longer, faster) {
		t.Fatal("duration change missed")
	}
	other := makeActivity(sample(1012, 10, 2, 200, "playing", "file:///two"))["timestamps"]
	if reflect.DeepEqual(other, longer) {
		t.Fatal("media change missed")
	}
	if makeActivity(sample(1013, 10, 2, 200, "buffering", "file:///two"))["timestamps"] != nil {
		t.Fatal("buffering clock retained")
	}
}

func TestFilenameParseCacheTracksPathAndPreservesReleaseGroups(t *testing.T) {
	c := &playbackClient{}
	first := c.parse("/media/Judas - SubsPlease - Show.S01E02.1080p.mkv")
	if first.Title != "Show" || first.Episode != 2 {
		t.Fatal(first)
	}
	if got := c.parse(c.parsedPath); got != first {
		t.Fatal("cached parsing changed")
	}
	second := c.parse("/media/Show.S01E03.mkv")
	if second.Episode != 3 || second == first {
		t.Fatal("new file reused old parse", second)
	}
}

func TestChapterCacheReuseReplacementFailureAndCancellation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "movie")
	if err := os.WriteFile(file, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	calls := 0
	cache := chapterCache{now: func() time.Time { return now }, probe: func(context.Context, string) ([]chapter, bool) {
		calls++
		return []chapter{{Start: "0", End: "10", Tags: map[string]string{"title": "Intro"}}}, true
	}}
	for i := 0; i < 3; i++ {
		if got := cache.get(context.Background(), file); len(got) != 1 {
			t.Fatal(got)
		}
	}
	if calls != 1 {
		t.Fatal("repeated probe", calls)
	}
	info, _ := os.Stat(file)
	replacement := file + ".new"
	os.WriteFile(replacement, []byte("two"), 0600)
	os.Chtimes(replacement, info.ModTime(), info.ModTime())
	if err := replaceFile(replacement, file); err != nil {
		t.Fatal(err)
	}
	cache.get(context.Background(), file)
	if calls != 2 {
		t.Fatal("replacement not detected")
	}
	now = now.Add(13 * time.Hour)
	cache.get(context.Background(), file)
	if calls != 3 {
		t.Fatal("expiry not detected")
	}
	failedCalls := 0
	failed := chapterCache{now: func() time.Time { return now }, probe: func(context.Context, string) ([]chapter, bool) { failedCalls++; return nil, false }}
	failed.get(context.Background(), file)
	failed.get(context.Background(), file)
	if failedCalls != 1 {
		t.Fatal("failure not cached")
	}
	now = now.Add(11 * time.Minute)
	failed.get(context.Background(), file)
	if failedCalls != 2 {
		t.Fatal("failed probe never retried")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelledCalls := 0
	cancelled := chapterCache{probe: func(context.Context, string) ([]chapter, bool) { cancelledCalls++; cancel(); return nil, false }}
	cancelled.get(ctx, file)
	cancelled.get(context.Background(), file)
	if cancelledCalls != 2 {
		t.Fatal("cancelled probe cached")
	}
}

func TestChapterCacheSharesConcurrentProbe(t *testing.T) {
	file := filepath.Join(t.TempDir(), "movie")
	os.WriteFile(file, []byte("one"), 0600)
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	cache := chapterCache{probe: func(context.Context, string) ([]chapter, bool) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []chapter{{Start: "0", End: "1"}}, true
	}}
	results := make(chan []chapter, 2)
	go func() { results <- cache.get(context.Background(), file) }()
	<-started
	go func() { results <- cache.get(context.Background(), file) }()
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case got := <-results:
			if len(got) != 1 {
				t.Fatal(got)
			}
		case <-time.After(time.Second):
			t.Fatal("probe waiter blocked")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate concurrent probe")
	}
}

func BenchmarkFilenameParsing(b *testing.B) {
	path := "/media/Movie (2024)/Judas - Show.S02E05.1080p.WEB-DL.x265.mkv"
	b.Run("parse", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			parseFilename(path)
		}
	})
	b.Run("cached", func(b *testing.B) {
		c := &playbackClient{}
		c.parse(path)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			c.parse(path)
		}
	})
}

func TestChapterCacheBoundAndCancelledWaiter(t *testing.T) {
	dir := t.TempDir()
	cache := chapterCache{probe: func(context.Context, string) ([]chapter, bool) { return nil, true }}
	for i := 0; i < 40; i++ {
		file := filepath.Join(dir, fmt.Sprint(i))
		os.WriteFile(file, []byte("video"), 0600)
		cache.get(context.Background(), file)
	}
	if len(cache.entries) > 32 {
		t.Fatal("unbounded chapter cache", len(cache.entries))
	}
	file := filepath.Join(dir, "0")
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	shared := chapterCache{probe: func(context.Context, string) ([]chapter, bool) { close(started); <-release; return nil, true }}
	go func() { shared.get(context.Background(), file); close(done) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if got := shared.get(ctx, file); got != nil {
		t.Fatal("cancelled waiter got labels")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("original probe blocked")
	}
}
