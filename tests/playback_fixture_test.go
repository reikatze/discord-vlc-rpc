package modules

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type playbackFixtureState struct {
	filename, uri, title, state string
	position, duration, rate    float64
}
type playbackHTTPFixture struct {
	mu       sync.Mutex
	state    playbackFixtureState
	server   *httptest.Server
	password string
}

func newPlaybackHTTPFixture(t *testing.T, state playbackFixtureState, password string) *playbackHTTPFixture {
	t.Helper()
	f := &playbackHTTPFixture{state: state, password: password}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, supplied, ok := r.BasicAuth()
		if !ok || supplied != f.password {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		s := f.state
		switch r.URL.Path {
		case "/requests/status.json":
			json.NewEncoder(w).Encode(map[string]any{
				"version": "3.0.24", "apiversion": 3, "state": s.state, "time": s.position, "length": s.duration, "rate": s.rate, "currentplid": 7,
				"information": map[string]any{"category": map[string]any{"meta": map[string]any{"filename": s.filename, "title": s.title}}},
			})
		case "/requests/playlist.json":
			json.NewEncoder(w).Encode(map[string]any{"children": []any{map[string]any{"id": "7", "uri": s.uri}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}
func (f *playbackHTTPFixture) options() map[string]string {
	return map[string]string{"http-port": strings.Split(f.server.URL, ":")[2], "http-password": f.password}
}
func (f *playbackHTTPFixture) playback(state string, position, rate float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.state, f.state.position, f.state.rate = state, position, rate
}
