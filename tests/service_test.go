package modules

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStandaloneHTTPToDiscordAndPrivacy(t *testing.T) {
	vlc := newPlaybackHTTPFixture(t, playbackFixtureState{filename: "Movie.2024.mkv", uri: "file:///private/Movie.2024.mkv", state: "playing", position: 10, duration: 100, rate: 1}, "")
	rpc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.Close()
	mock := newDiscordIPCMock(t, rpc, true)
	p := testPaths(t)
	cfg := defaults()
	cfg.ApplicationID = "123456789"
	if err := saveSettings(p, cfg); err != nil {
		t.Fatal(err)
	}
	atomicWrite(filepath.Join(p.config, "vlcrc"), []byte("http-port="+strings.Split(vlc.server.URL, ":")[2]+"\n"), 0600)
	s := &service{paths: p, refresh: make(chan struct{}, 1), buildIndex: make(chan struct{}, 1), rpcDial: func() (net.Conn, error) { return net.DialTimeout("tcp", rpc.Addr().String(), time.Second) }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("service did not stop")
		}
	}()
	await := func(predicate func(map[string]any) bool) {
		mock.await(t, func(frame ipcMockFrame) bool {
			activity, ok := mockActivity(frame)
			return ok && predicate(activity)
		})
	}
	await(func(a map[string]any) bool { return a != nil && a["details"] == "Movie" && a["timestamps"] != nil })
	vlc.playback("paused", 10, 1)
	poke(s.refresh)
	await(func(a map[string]any) bool { return a != nil && a["state"] == "Paused" && a["timestamps"] == nil })
	c, _ := loadSettings(p)
	c.Ignored = []string{"/private"}
	saveSettings(p, c)
	poke(s.refresh)
	await(func(a map[string]any) bool { return a == nil })
	for len(mock.frames) > 0 {
		<-mock.frames
	}
	poke(s.refresh)
	select {
	case frame := <-mock.frames:
		if a, ok := mockActivity(frame); ok && a != nil {
			t.Fatal("private activity returned")
		}
	case <-time.After(700 * time.Millisecond):
	}
}

func TestLookupMediaIdentityReusesKeyAndTracksInputs(t *testing.T) {
	identity := &lookupMediaIdentity{}
	parsed := parsedMedia{Title: "Example Show", TV: true, Season: 1, Episode: 1}
	cfg := defaults()
	uri := "file:///media/Example.Show.S01E01.mkv"
	want := uri + "\x00" + cacheKey(parsed, cfg)
	if identity.key(uri, parsed, cfg) != want {
		t.Fatal("media key changed")
	}
	if allocations := testing.AllocsPerRun(100, func() { identity.key(uri, parsed, cfg) }); allocations != 0 {
		t.Fatal("warm media identity still hashes/encodes", allocations)
	}
	for _, edit := range []func(*settings){
		func(c *settings) { c.Language = "fr-FR" }, func(c *settings) { c.PosterFit = "raw" },
		func(c *settings) { c.EpisodeLookup = !c.EpisodeLookup }, func(c *settings) { c.IndexEnabled = !c.IndexEnabled }, func(c *settings) { c.CacheDays++ },
	} {
		changed := cfg
		edit(&changed)
		if identity.key(uri, parsed, changed) != uri+"\x00"+cacheKey(parsed, changed) {
			t.Fatal("metadata input did not invalidate media identity")
		}
	}
	parsed.Episode++
	if identity.key(uri, parsed, cfg) != uri+"\x00"+cacheKey(parsed, cfg) {
		t.Fatal("parsed media change not reflected")
	}
	uri += ".new"
	if identity.key(uri, parsed, cfg) != uri+"\x00"+cacheKey(parsed, cfg) {
		t.Fatal("URI change not reflected")
	}
}

func TestServiceHonorsEpisodeRefreshExpiry(t *testing.T) {
	p := testPaths(t)
	cfg := defaults()
	cfg.APIKey = "fixture-key"
	cfg.IndexEnabled = false
	if err := saveSettings(p, cfg); err != nil {
		t.Fatal(err)
	}
	vlc := newPlaybackHTTPFixture(t, playbackFixtureState{filename: "Show.S01E01.mkv", uri: "file:///media/Show.S01E01.mkv", state: "playing", position: 10, duration: 100, rate: 1}, "")
	if err := atomicWrite(filepath.Join(p.config, "vlcrc"), []byte("http-port="+vlc.options()["http-port"]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan struct{}, 8)
	var calls atomic.Int32
	s := &service{paths: p, refresh: make(chan struct{}, 1), buildIndex: make(chan struct{}, 1), clearMetadata: make(chan struct{}, 1), rpcDial: func() (net.Conn, error) { return nil, errors.New("test offline") }}
	s.lookup = func(context.Context, paths, settings, parsedMedia, string) (*metadataHit, []chapter, error) {
		expires := time.Now().Add(time.Hour)
		if calls.Add(1) == 1 {
			expires = time.Now().Add(2 * time.Second)
		}
		requests <- struct{}{}
		return &metadataHit{Title: "Show", Episode: "01 of 2: One", Complete: true, RefreshAt: expires.Unix()}, nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("service did not stop")
		}
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-requests:
		case <-time.After(5 * time.Second):
			t.Fatal("service retained an expired episode count")
		}
	}
	poke(s.clearMetadata)
	select {
	case <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("clear-cache action retained the in-memory match")
	}
}

func TestCancelledLookupCannotReplaceSameMediaRequest(t *testing.T) {
	vlc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/requests/status.json" {
			fmt.Fprint(w, `{"version":"3","apiversion":3,"state":"playing","time":10,"length":100,"rate":1,"currentplid":1,"information":{"category":{"meta":{"filename":"Movie.mkv"}}}}`)
		} else {
			fmt.Fprint(w, `{"children":[{"id":1,"uri":"file:///media/Movie.mkv"}]}`)
		}
	}))
	defer vlc.Close()
	p := testPaths(t)
	if err := initSettings(p); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(filepath.Join(p.config, "vlcrc"), []byte("http-port="+strings.Split(vlc.URL, ":")[2]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	type request struct {
		ctx      context.Context
		reply    chan string
		returned chan struct{}
	}
	requests := make(chan request, 8)
	s := &service{paths: p, refresh: make(chan struct{}, 1), buildIndex: make(chan struct{}, 1), rpcDial: func() (net.Conn, error) { return nil, errors.New("test offline") }}
	s.lookup = func(ctx context.Context, _ paths, _ settings, _ parsedMedia, _ string) (*metadataHit, []chapter, error) {
		q := request{ctx, make(chan string, 1), make(chan struct{})}
		requests <- q
		// Deliberately ignore cancellation to simulate a result already in flight.
		name := <-q.reply
		close(q.returned)
		return &metadataHit{Title: name, Complete: true}, nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("shutdown blocked")
		}
	}()
	next := func() request {
		select {
		case q := <-requests:
			return q
		case <-time.After(5 * time.Second):
			t.Fatal("lookup did not start")
			return request{}
		}
	}
	old := next()
	poke(s.refresh)
	current := next()
	defer func() {
		select {
		case old.reply <- "cleanup":
		default:
		}
		select {
		case current.reply <- "cleanup":
		default:
		}
	}()
	select {
	case <-old.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("old lookup not cancelled")
	}
	old.reply <- "Stale title"
	<-old.returned
	deadline := time.After(800 * time.Millisecond)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
checking:
	for {
		select {
		case <-deadline:
			break checking
		case <-ticker.C:
			playback, _, _, _, _ := s.details()
			if strings.Contains(playback, "Stale title") {
				t.Fatal("cancelled result replaced current lookup")
			}
			select {
			case <-current.ctx.Done():
				t.Fatal("current lookup cancelled by stale result")
			default:
			}
			select {
			case <-requests:
				t.Fatal("stale result triggered another lookup")
			default:
			}
		}
	}
	current.reply <- "Current title"
	deadline = time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("current result not applied")
		case <-ticker.C:
			playback, _, _, _, _ := s.details()
			if strings.Contains(playback, "Current title") {
				return
			}
		}
	}
}

func TestVLCPollBackoffAndRecovery(t *testing.T) {
	p := vlcPollBackoff{}
	if p.interval() != 500*time.Millisecond {
		t.Fatal("active interval")
	}
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second} {
		p.failed()
		if p.interval() != want {
			t.Fatal(p.interval(), want)
		}
	}
	p.success()
	if p.interval() != 500*time.Millisecond {
		t.Fatal("recovery did not restore active interval")
	}
	p.stopped()
	if p.interval() != 2*time.Second {
		t.Fatal("stopped interval")
	}
	p.success()
	if p.interval() != 500*time.Millisecond {
		t.Fatal("stopped recovery")
	}
}

func TestPollingBackoffRefreshAndActiveRecovery(t *testing.T) {
	var recovered atomic.Bool
	samples := make(chan time.Time, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/requests/status.json" {
			samples <- time.Now()
			if !recovered.Load() {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `{"version":"3","apiversion":3,"state":"playing","time":10,"length":100,"rate":1,"currentplid":1,"information":{"category":{"meta":{"filename":"Movie.mkv"}}}}`)
		} else {
			fmt.Fprint(w, `{"children":[{"id":1,"uri":"file:///media/Movie.mkv"}]}`)
		}
	}))
	defer server.Close()
	p := testPaths(t)
	initSettings(p)
	atomicWrite(filepath.Join(p.config, "vlcrc"), []byte("http-port="+strings.Split(server.URL, ":")[2]+"\n"), 0600)
	s := &service{paths: p, refresh: make(chan struct{}, 1), buildIndex: make(chan struct{}, 1), rpcDial: func() (net.Conn, error) { return nil, errors.New("test offline") }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("shutdown blocked")
		}
	}()
	next := func(timeout time.Duration) time.Time {
		select {
		case at := <-samples:
			return at
		case <-time.After(timeout):
			t.Fatal("missing HTTP sample")
			return time.Time{}
		}
	}
	first, second := next(5*time.Second), next(5*time.Second)
	if second.Sub(first) < 900*time.Millisecond {
		t.Fatal("failure did not back off", second.Sub(first))
	}
	// Wait for the second failed response to finish before explicitly waking the loop.
	limit := time.Now().Add(time.Second)
	for time.Now().Before(limit) {
		playback, _, _, _, _ := s.details()
		if playback == "VLC HTTP status failed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	recovered.Store(true)
	at := time.Now()
	poke(s.refresh)
	third := next(time.Second)
	if third.Sub(at) > time.Second {
		t.Fatal("refresh waited for backoff")
	}
	fourth := next(time.Second)
	if fourth.Sub(third) < 400*time.Millisecond || fourth.Sub(third) > time.Second {
		t.Fatal("active polling interval not restored", fourth.Sub(third))
	}
}

func TestStoppedPollingRefreshAndPlaybackRecovery(t *testing.T) {
	var playing atomic.Bool
	samples := make(chan time.Time, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/requests/status.json" {
			samples <- time.Now()
			state := "stopped"
			if playing.Load() {
				state = "playing"
			}
			fmt.Fprintf(w, `{"version":"3","apiversion":3,"state":%q,"time":10,"length":100,"rate":1,"currentplid":1,"information":{"category":{"meta":{"filename":"Movie.mkv"}}}}`, state)
		} else {
			fmt.Fprint(w, `{"children":[{"id":1,"uri":"file:///media/Movie.mkv"}]}`)
		}
	}))
	defer server.Close()
	p := testPaths(t)
	if err := initSettings(p); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(filepath.Join(p.config, "vlcrc"), []byte("http-port="+strings.Split(server.URL, ":")[2]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &service{paths: p, refresh: make(chan struct{}, 1), buildIndex: make(chan struct{}, 1), rpcDial: func() (net.Conn, error) { return nil, errors.New("test offline") }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("shutdown blocked")
		}
	}()
	next := func(timeout time.Duration) time.Time {
		select {
		case at := <-samples:
			return at
		case <-time.After(timeout):
			t.Fatal("missing VLC sample")
			return time.Time{}
		}
	}
	first, second := next(5*time.Second), next(5*time.Second)
	if second.Sub(first) < 1800*time.Millisecond {
		t.Fatal("stopped polling not slowed", second.Sub(first))
	}
	// Wait until the second stopped sample has been applied before waking it.
	limit := time.Now().Add(time.Second)
	for time.Now().Before(limit) {
		playback, _, _, _, _ := s.details()
		if playback == "Idle" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	playing.Store(true)
	at := time.Now()
	poke(s.refresh)
	third := next(time.Second)
	if third.Sub(at) > time.Second {
		t.Fatal("refresh waited for stopped interval")
	}
	fourth := next(1500 * time.Millisecond)
	if fourth.Sub(third) < 400*time.Millisecond || fourth.Sub(third) > 1500*time.Millisecond {
		t.Fatal("playback interval not restored", fourth.Sub(third))
	}
}

func TestMetadataLookupSurvivesPresentationSettingsEdits(t *testing.T) {
	vlc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/requests/status.json" {
			fmt.Fprint(w, `{"version":"3","apiversion":3,"state":"playing","time":10,"length":100,"rate":1,"currentplid":1,"information":{"category":{"meta":{"filename":"Movie.mkv"}}}}`)
		} else {
			fmt.Fprint(w, `{"children":[{"id":1,"uri":"file:///media/Movie.mkv"}]}`)
		}
	}))
	defer vlc.Close()
	p := testPaths(t)
	if err := initSettings(p); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(filepath.Join(p.config, "vlcrc"), []byte("http-port="+strings.Split(vlc.URL, ":")[2]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	type request struct {
		ctx    context.Context
		config settings
		reply  chan string
	}
	requests := make(chan request, 8)
	s := &service{paths: p, refresh: make(chan struct{}, 1), buildIndex: make(chan struct{}, 1), rpcDial: func() (net.Conn, error) { return nil, errors.New("test offline") }}
	s.lookup = func(ctx context.Context, _ paths, c settings, _ parsedMedia, _ string) (*metadataHit, []chapter, error) {
		q := request{ctx, c, make(chan string, 1)}
		select {
		case requests <- q:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		select {
		case title := <-q.reply:
			return &metadataHit{Title: title, Complete: true}, nil, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("service did not stop")
		}
	}()
	next := func() request {
		t.Helper()
		select {
		case q := <-requests:
			return q
		case <-time.After(5 * time.Second):
			t.Fatal("expected metadata lookup")
			return request{}
		}
	}
	save := func(c settings) {
		t.Helper()
		if err := saveSettings(p, c); err != nil {
			t.Fatal(err)
		}
		// Do not use Refresh: direct JSON edits must preserve lookup work.
	}
	unchanged := func(pending context.Context, title string) {
		t.Helper()
		timer := time.NewTimer(1200 * time.Millisecond)
		defer timer.Stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-timer.C:
				return
			case <-pending.Done():
				t.Fatal("presentation edit cancelled a pending lookup")
			case <-requests:
				t.Fatal("presentation edit started another lookup")
			case <-ticker.C:
				playback, _, _, _, _ := s.details()
				if title != "" && !strings.Contains(playback, title) {
					t.Fatal("presentation edit discarded the match:", playback)
				}
			}
		}
	}
	old := next()
	c, err := loadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	c.StartupNotification = false
	c.ApplicationID = "123456789"
	c.LargeImage, c.LargeText = "custom", "Updated hover text"
	c.SmallPlaying, c.SmallPaused, c.SmallIdle = "play", "pause", "idle"
	c.Ignored = []string{"/unrelated/private"}
	save(c)
	unchanged(old.ctx, "")
	c.Language = "fr-FR"
	save(c)
	current := next()
	if current.config.Language != "fr-FR" {
		t.Fatal("lookup did not use the new language")
	}
	select {
	case <-old.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("metadata edit did not cancel stale work")
	}
	current.reply <- "Matched title"
	deadline := time.Now().Add(5 * time.Second)
	for {
		playback, _, _, _, _ := s.details()
		if strings.Contains(playback, "Matched title") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lookup result not applied")
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.StartupNotification = true
	c.ApplicationID = "987654321"
	c.LargeText = "Another hover text"
	save(c)
	unchanged(context.Background(), "Matched title")
	c.APIKey = "new-test-key"
	c.IndexEnabled = false // Keep automatic database downloads disabled in this fixture.
	save(c)
	updated := next()
	if updated.config.APIKey != c.APIKey {
		t.Fatal("credential edit did not restart lookup")
	}
}

func TestMetadataConfigurationInputs(t *testing.T) {
	p := testPaths(t)
	base := defaults()
	metadataEdits := map[string]func(*settings){
		"credential":   func(c *settings) { c.APIKey = "new-key" },
		"language":     func(c *settings) { c.Language = "fr-FR" },
		"episode":      func(c *settings) { c.EpisodeLookup = false },
		"poster":       func(c *settings) { c.PosterFit = "raw" },
		"lifetime":     func(c *settings) { c.CacheDays++ },
		"index":        func(c *settings) { c.IndexEnabled = false },
		"index folder": func(c *settings) { c.IndexPath = filepath.Join(t.TempDir(), "index") },
		"cache folder": func(c *settings) { c.CachePath = filepath.Join(t.TempDir(), "cache") },
	}
	for name, edit := range metadataEdits {
		t.Run(name, func(t *testing.T) {
			c := base
			edit(&c)
			if metadataConfiguration(p, c) == metadataConfiguration(p, base) {
				t.Fatal("metadata edit was ignored")
			}
		})
	}
	// Explicitly selecting the default storage location does not change lookup inputs.
	c := base
	c.IndexPath, c.CachePath = p.indexFolder(base), p.cacheFolder(base)
	if metadataConfiguration(p, c) != metadataConfiguration(p, base) {
		t.Fatal("equivalent storage paths invalidate lookups")
	}
}
