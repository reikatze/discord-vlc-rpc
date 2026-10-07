//go:build !windows

package modules

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"

	"testing"
	"time"
)

func TestRPCHandshakeDeduplicateClearReconnect(t *testing.T) {
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	activities := make(chan map[string]any, 10)
	failures := make(chan error, 2)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for sessions := 0; sessions < 2; sessions++ {
			c, e := server.Accept()
			if e != nil {
				failures <- e
				return
			}
			func() {
				defer c.Close()
				read := func() (map[string]any, error) {
					h := make([]byte, 8)
					if _, e := io.ReadFull(c, h); e != nil {
						return nil, e
					}
					b := make([]byte, binary.LittleEndian.Uint32(h[4:]))
					if _, e := io.ReadFull(c, b); e != nil {
						return nil, e
					}
					v := map[string]any{}
					e := json.Unmarshal(b, &v)
					return v, e
				}
				if _, e = read(); e != nil {
					failures <- e
					return
				}
				b := []byte(`{"evt":"READY"}`)
				h := make([]byte, 8)
				binary.LittleEndian.PutUint32(h, 1)
				binary.LittleEndian.PutUint32(h[4:], uint32(len(b)))
				c.Write(append(h, b...))
				for {
					v, e := read()
					if e != nil {
						return
					}
					select {
					case activities <- v:
					case <-stop:
						return
					}
				}
			}()
		}
	}()
	r := &discordRPC{dial: func() (net.Conn, error) { return net.DialTimeout("tcp", server.Addr().String(), time.Second) }}
	defer r.close()
	body, err := json.Marshal(makeActivity(exampleSnapshot()))
	if err != nil {
		t.Fatal(err)
	}
	activity := string(body)
	r.update("123", activity)
	receive := func() map[string]any {
		select {
		case v := <-activities:
			return v
		case e := <-failures:
			t.Fatal(e)
		case <-time.After(2 * time.Second):
			t.Fatal("missing SET_ACTIVITY")
		}
		return nil
	}
	if receive()["cmd"] != "SET_ACTIVITY" {
		t.Fatal("command")
	}
	r.update("123", activity)
	select {
	case <-activities:
		t.Fatal("duplicate activity sent")
	case <-time.After(20 * time.Millisecond):
	}
	r.update("123", "null")
	if receive()["args"].(map[string]any)["activity"] != nil {
		t.Fatal("presence not cleared")
	}
	r.close()
	r.update("123", activity)
	if receive()["args"].(map[string]any)["activity"] == nil {
		t.Fatal("reconnection did not replay")
	}
}

func TestAutostartArgumentEscaping(t *testing.T) {
	got := desktopArg("/path with spaces/100%/$app")
	want := `"/path with spaces/100%%/\\$app"`
	if got != want {
		t.Fatalf("desktop argument %q, want %q", got, want)
	}
}

func TestDiscordSocketDiscoveryDeadlineAndCancellation(t *testing.T) {
	var discovery discordSocketDiscovery
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	calls := 0
	started := time.Now()
	_, err := discovery.connect(ctx, []string{"/a", "/b", "/c"}, func(ctx context.Context, network, path string) (net.Conn, error) {
		calls++
		// Simulate a stalled socket while respecting the per-attempt limit.
		attempt, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		<-attempt.Done()
		return nil, attempt.Err()
	})
	if err == nil || calls > 3 || calls < 1 || time.Since(started) > time.Second {
		t.Fatalf("unbounded search: calls=%d elapsed=%v err=%v", calls, time.Since(started), err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	_, err = discovery.connect(cancelled, []string{"/a"}, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dial after cancellation")
		return nil, nil
	})
	if err == nil {
		t.Fatal("cancelled discovery succeeded")
	}
}

func TestDiscordSocketDiscoveryCachedPathAndFallback(t *testing.T) {
	var discovery discordSocketDiscovery
	dirs := []string{"", "/one", "/one", "/one/", "/two"}
	target := filepath.Join("/two", "snap.discord", "discord-ipc-9")
	var attempts []string
	success := func(ctx context.Context, network, path string) (net.Conn, error) {
		attempts = append(attempts, path)
		if path == target {
			a, b := net.Pipe()
			b.Close()
			return a, nil
		}
		return nil, errors.New("unavailable")
	}
	c, err := discovery.connect(context.Background(), dirs, success)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if len(attempts) != 60 {
		t.Fatalf("duplicate directory searched: %d", len(attempts))
	}
	attempts = nil
	c, err = discovery.connect(context.Background(), dirs, success)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if len(attempts) != 1 || attempts[0] != target {
		t.Fatal("cached socket not tried first", attempts)
	}
	// The cached socket disappears; scan each candidate once and remember its replacement.
	target = filepath.Join("/one", "discord-ipc-0")
	attempts = nil
	c, err = discovery.connect(context.Background(), dirs, success)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if len(attempts) != 2 || discovery.last != target {
		t.Fatal("fallback did not replace cache", attempts)
	}
	// Cached locations outside a changed environment are not used.
	attempts = nil
	_, err = discovery.connect(context.Background(), []string{"/other"}, success)
	if err == nil || discovery.last != "" || len(attempts) != 30 {
		t.Fatal("stale scope/cache", attempts)
	}
	seen := map[string]bool{}
	for _, p := range attempts {
		if seen[p] {
			t.Fatal("duplicate candidate", p)
		}
		seen[p] = true
	}
}
