package modules

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStandaloneHTTPToDiscordAndPrivacy(t *testing.T) {
	var mu sync.Mutex
	state := "playing"
	vlc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/requests/status.json" {
			fmt.Fprintf(w, `{"version":"3.0.24","apiversion":3,"state":%q,"time":10,"length":100,"rate":1,"currentplid":1,"information":{"category":{"meta":{"filename":"Movie.2024.mkv"}}}}`, state)
		} else {
			fmt.Fprint(w, `{"children":[{"id":1,"uri":"file:///private/Movie.2024.mkv"}]}`)
		}
	}))
	defer vlc.Close()
	rpc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.Close()
	activities := make(chan map[string]any, 16)
	go func() {
		connection, err := rpc.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		read := func() (map[string]any, error) {
			header := make([]byte, 8)
			if _, e := io.ReadFull(connection, header); e != nil {
				return nil, e
			}
			raw := make([]byte, binary.LittleEndian.Uint32(header[4:]))
			if _, e := io.ReadFull(connection, raw); e != nil {
				return nil, e
			}
			value := map[string]any{}
			e := json.Unmarshal(raw, &value)
			return value, e
		}
		if _, e := read(); e != nil {
			return
		}
		ready := []byte(`{"evt":"READY"}`)
		header := make([]byte, 8)
		binary.LittleEndian.PutUint32(header, 1)
		binary.LittleEndian.PutUint32(header[4:], uint32(len(ready)))
		connection.Write(append(header, ready...))
		for {
			value, e := read()
			if e != nil {
				return
			}
			args, _ := value["args"].(map[string]any)
			activity, _ := args["activity"].(map[string]any)
			select {
			case activities <- activity:
			default:
			}
		}
	}()
	p := paths{config: t.TempDir()}
	initSettings(p)
	atomicWrite(filepath.Join(p.config, "vlcrc"), []byte("http-port="+strings.Split(vlc.URL, ":")[2]+"\n"), 0600)
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
		deadline := time.After(5 * time.Second)
		for {
			select {
			case activity := <-activities:
				if predicate(activity) {
					return
				}
			case <-deadline:
				t.Fatal("expected Discord activity was not received")
			}
		}
	}
	await(func(a map[string]any) bool { return a != nil && a["details"] == "Movie" && a["timestamps"] != nil })
	mu.Lock()
	state = "paused"
	mu.Unlock()
	poke(s.refresh)
	await(func(a map[string]any) bool { return a != nil && a["state"] == "Paused" && a["timestamps"] == nil })
	c, _ := loadSettings(p)
	c.Ignored = []string{"/private"}
	saveSettings(p, c)
	poke(s.refresh)
	await(func(a map[string]any) bool { return a == nil })
	for len(activities) > 0 {
		<-activities
	}
	poke(s.refresh)
	select {
	case a := <-activities:
		if a != nil {
			t.Fatal("private activity returned")
		}
	case <-time.After(700 * time.Millisecond):
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
	p := paths{config: t.TempDir()}
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
	p := paths{config: t.TempDir()}
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
