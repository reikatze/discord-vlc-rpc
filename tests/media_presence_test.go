package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type presenceFixtureTransport struct{ base http.RoundTripper }

func (f presenceFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "wsrv.nl" && r.Method == http.MethodHead {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/jpeg"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	return f.base.RoundTrip(r)
}

// These exercise production sampling, filename parsing, TMDb lookup, enrichment,
// timestamps, JSON encoding and Discord framing; no public services are required.
func TestMediaMetadataToDiscordPresence(t *testing.T) {
	testMediaMetadataToDiscordPresence(t, false)
}

func TestNativeDiscordIPCMetadataPresence(t *testing.T) {
	requireDesktopIntegration(t)
	testMediaMetadataToDiscordPresence(t, true)
}

func testMediaMetadataToDiscordPresence(t *testing.T, native bool) {
	for _, tv := range []bool{false, true} {
		for _, fit := range []string{"raw", "contain"} {
			kind, title, filename, id, year := "movie", "Arrival", "Arrival.2016.mkv", 329865, "2016"
			if tv {
				kind, title, filename, id, year = "tv", "Dark", "Dark.S02E05.mkv", 70523, "2017"
			}
			t.Run(kind+"/"+fit, func(t *testing.T) {
				p := testPaths(t)
				cfg := defaults()
				cfg.APIKey, cfg.IndexEnabled, cfg.PosterFit = "fixture-key", false, fit
				cfg.LargeImage, cfg.LargeText = "fallback-art", "VLC"
				cfg.SmallPlaying, cfg.SmallPaused, cfg.SmallIdle = "playing-badge", "paused-badge", "idle-badge"
				vlc := newPlaybackHTTPFixture(t, playbackFixtureState{filename: filename, uri: "file:///media/" + filename, title: "Incorrect embedded title", state: "playing", position: 120, duration: 1800, rate: 1}, "fixture-password")
				tmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("api_key") != cfg.APIKey || r.URL.Query().Get("language") != cfg.Language {
						http.Error(w, "bad metadata credentials or language", http.StatusUnauthorized)
						return
					}
					switch r.URL.Path {
					case "/search/" + kind:
						if r.URL.Query().Get("query") != title {
							http.Error(w, "incorrect filename query", http.StatusBadRequest)
							return
						}
						fmt.Fprintf(w, `{"results":[{"id":%d,"title":%q,"name":%q,"release_date":%q,"first_air_date":%q}]}`, id, title, title, year+"-01-01", year+"-01-01")
					case "/search/tv":
						fmt.Fprint(w, `{"results":[]}`)
					case fmt.Sprintf("/%s/%d", kind, id):
						fmt.Fprintf(w, `{"id":%d,"title":%q,"name":%q,"release_date":%q,"first_air_date":%q,"poster_path":"/poster.jpg","genres":[{"name":"Science Fiction"},{"name":"Drama"}]}`, id, title, title, year+"-01-01", year+"-01-01")
					case "/tv/70523/season/2":
						fmt.Fprint(w, `{"episodes":[{},{},{},{},{"season_number":2,"episode_number":5,"name":"Lost and Found","still_path":"/episode-5.jpg"},{},{},{}]}`)
					default:
						http.NotFound(w, r)
					}
				}))
				defer tmdb.Close()
				metadata := newTMDB(p, cfg)
				metadata.base = tmdb.URL
				metadata.client = &http.Client{Transport: presenceFixtureTransport{tmdb.Client().Transport}, Timeout: time.Second}
				var listener net.Listener
				var dial func() (net.Conn, error)
				if native {
					listener = nativeDiscordListener(t)
				} else {
					var err error
					listener, err = net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					dial = func() (net.Conn, error) { return net.DialTimeout("tcp", listener.Addr().String(), time.Second) }
				}
				mock := newDiscordIPCMock(t, listener, true)
				rpc := &discordRPC{dial: dial}
				defer rpc.close()
				now := time.Unix(1700000000, 0)
				playback := &playbackClient{now: func() time.Time { return now }}
				options := vlc.options()
				for _, step := range []struct {
					state          string
					position, rate float64
				}{
					{"playing", 120, 1}, {"playing", 600, 1}, {"paused", 600, 1},
					{"playing", 600, 2}, {"buffering", 610, 2}, {"playing", 610, 2},
				} {
					vlc.playback(step.state, step.position, step.rate)
					// Fixed time makes current and remaining-time assertions exact, not sleep-based.
					snapshot, parsed, err := playback.sample(context.Background(), options, cfg)
					if err != nil || snapshot == nil {
						t.Fatalf("sample: %v", err)
					}
					hit, err := metadata.lookup(context.Background(), parsed)
					if err != nil || hit == nil || !hit.Complete {
						t.Fatalf("metadata: %+v, %v", hit, err)
					}
					enrich(snapshot, hit)
					body, err := json.Marshal(makeActivity(snapshot))
					if err != nil {
						t.Fatal(err)
					}
					rpc.update(integrationApplicationID, string(body))
					frame := mock.await(t, func(f ipcMockFrame) bool { _, ok := mockActivity(f); return ok })
					actual, _ := mockActivity(frame)
					link := fmt.Sprintf("https://www.themoviedb.org/%s/%d", kind, id)
					second, stateURL := year+" · Science Fiction, Drama", link
					poster := "https://image.tmdb.org/t/p/w500/poster.jpg"
					if fit == "contain" {
						poster = "https://wsrv.nl/?url=" + url.QueryEscape(poster) + "&w=512&h=512&fit=contain&bg=000000"
					}
					if tv {
						second = "05 of 8: Lost and Found"
						stateURL += "/season/2/episode/5"
						poster = "https://image.tmdb.org/t/p/w780/episode-5.jpg"
					}
					badge, label := "playing-badge", "Playing"
					if step.state == "paused" {
						badge, label, second = "paused-badge", "Paused", "Paused"
					}
					if step.state == "buffering" {
						badge, label, second = "paused-badge", "Buffering", "Buffering"
					}
					want := map[string]any{"type": float64(3), "name": title, "status_display_type": float64(2), "details": title, "state": second, "details_url": link, "state_url": stateURL,
						"assets": map[string]any{"large_image": poster, "large_text": title, "large_url": link, "small_image": badge, "small_text": label}}
					if step.state == "playing" {
						start := float64(now.Unix()) - step.position/step.rate
						end := float64(now.Unix()) + (1800-step.position)/step.rate
						want["timestamps"] = map[string]any{"start": start, "end": end}
					}
					if !reflect.DeepEqual(actual, want) {
						t.Fatalf("%s position=%g rate=%g\ngot:  %#v\nwant: %#v", step.state, step.position, step.rate, actual, want)
					}
					args := frame.payload["args"].(map[string]any)
					if number(args, "pid") != float64(os.Getpid()) || text(frame.payload, "nonce") == "" {
						t.Fatal("missing Discord process ID or nonce", frame.payload)
					}
				}
			})
		}
	}
}
