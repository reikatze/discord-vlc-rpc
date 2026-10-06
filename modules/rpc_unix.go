//go:build !windows

package modules

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type discordSocketDiscovery struct {
	mu   sync.Mutex
	last string
}

var discordSockets discordSocketDiscovery

func connectDiscord() (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	dialer := &net.Dialer{Timeout: 50 * time.Millisecond}
	return discordSockets.connect(ctx, []string{os.Getenv("XDG_RUNTIME_DIR"), os.Getenv("TMPDIR"), os.Getenv("TMP"), os.Getenv("TEMP"), "/tmp"}, dialer.DialContext)
}

func (d *discordSocketDiscovery) connect(ctx context.Context, dirs []string, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	d.mu.Lock()
	last := d.last
	d.mu.Unlock()
	paths := make([]string, 0, 150)
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		for i := 0; i < 10; i++ {
			for _, sub := range []string{"", "app/com.discordapp.Discord", "snap.discord"} {
				paths = append(paths, filepath.Join(dir, sub, fmt.Sprintf("discord-ipc-%d", i)))
			}
		}
	}
	// Only reuse a cached path while it remains in the current search scope.
	for i, path := range paths {
		if path == last {
			copy(paths[1:i+1], paths[:i])
			paths[0] = path
			break
		}
	}
	for _, path := range paths {
		if ctx.Err() != nil {
			break
		}
		c, err := dial(ctx, "unix", path)
		if err == nil {
			d.mu.Lock()
			d.last = path
			d.mu.Unlock()
			return c, nil
		}
	}
	d.mu.Lock()
	if d.last == last {
		d.last = ""
	}
	d.mu.Unlock()
	return nil, errors.New("Discord desktop unavailable")
}
