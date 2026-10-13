package modules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

type vlcStatus struct {
	vlcIdentity
	Time, Length, Rate float64
	Current            int `json:"currentplid"`
	Information        struct {
		Category map[string]map[string]any `json:"category"`
		Chapter  int                       `json:"chapter"`
		Chapters json.RawMessage           `json:"chapters"`
	} `json:"information"`
}
type playlistNode struct {
	ID       json.Number    `json:"id"`
	URI      string         `json:"uri"`
	Children []playlistNode `json:"children"`
}
type playbackClient struct {
	uri, identity string
	pollPlaylist  time.Time
	client        *http.Client
	parsedPath    string
	parsed        parsedMedia
	directoryPath string
	directory     directoryTitle
	ignored       ignoredPathCache
	clock         playbackClock
	now           func() time.Time
}

func findURI(n playlistNode, id int) string {
	if n.ID.String() == fmt.Sprint(id) && n.URI != "" {
		return n.URI
	}
	for _, child := range n.Children {
		if uri := findURI(child, id); uri != "" {
			return uri
		}
	}
	return ""
}
func localMediaPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || strings.ToLower(u.Scheme) != "file" {
		return "", false
	}
	path := u.Path
	if u.Host != "" && u.Host != "localhost" {
		path = "//" + u.Host + path
	}
	if len(path) > 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), path != ""
}
func cleanIgnoredPath(s string) string {
	s = strings.ReplaceAll(s, `\`, "/")
	s = filepath.ToSlash(filepath.Clean(s))
	if runtime.GOOS == "windows" || len(s) > 1 && s[1] == ':' || strings.HasPrefix(s, "//") {
		s = strings.ToLower(s)
	}
	return strings.TrimRight(s, "/")
}

type ignoredPathCache struct {
	loaded                 bool
	configured, normalized []string
	path                   string
	ignored                bool
}

func (c *ignoredPathCache) contains(path string, configured []string) bool {
	changed := !c.loaded || !slices.Equal(c.configured, configured)
	if changed {
		c.configured = slices.Clone(configured)
		c.normalized = make([]string, 0, len(configured))
		for _, entry := range configured {
			if p, ok := localMediaPath(entry); ok {
				entry = p
			}
			if entry = cleanIgnoredPath(entry); entry != "" {
				c.normalized = append(c.normalized, entry)
			}
		}
	}
	if changed || path != c.path {
		c.path, c.ignored, c.loaded = path, false, true
		clean := cleanIgnoredPath(path)
		for _, entry := range c.normalized {
			if clean == entry || strings.HasPrefix(clean, entry+"/") {
				c.ignored = true
				break
			}
		}
	}
	return c.ignored
}
func (c *playbackClient) sample(ctx context.Context, options map[string]string, config settings) (*Snapshot, parsedMedia, error) {
	if c.client == nil {
		c.client = localHTTPClient()
	}
	var status vlcStatus
	if err := vlcHTTP(ctx, c.client, options, "status.json", &status); err != nil {
		c.clock = playbackClock{}
		c.uri = ""
		return nil, parsedMedia{}, err
	}
	if !status.valid() {
		return nil, parsedMedia{}, errors.New("Endpoint is not VLC")
	}
	if status.State == "stopped" {
		c.clock = playbackClock{}
		c.uri = ""
		return &Snapshot{Version: 3, Updated: float64(c.time().UnixNano()) / 1e9, Media: map[string]any{"title": "VLC", "state": "idle"}, Playback: map[string]any{}, Options: activityOptions(config)}, parsedMedia{}, nil
	}
	if status.State != "playing" && status.State != "paused" && status.State != "buffering" {
		return nil, parsedMedia{}, errors.New("Unknown VLC playback state")
	}
	meta := status.Information.Category["meta"]
	identity := fmt.Sprint(status.Current) + ":" + text(meta, "filename")
	if identity != c.identity || time.Since(c.pollPlaylist) > 2*time.Second {
		var playlist playlistNode
		if err := vlcHTTP(ctx, c.client, options, "playlist.json", &playlist); err != nil {
			c.uri = ""
			return nil, parsedMedia{}, err
		}
		c.uri = findURI(playlist, status.Current)
		c.identity = identity
		c.pollPlaylist = time.Now()
	}
	path, ok := localMediaPath(c.uri)
	if !ok || c.ignored.contains(path, config.Ignored) {
		c.clock = playbackClock{}
		return nil, parsedMedia{}, nil
	}
	parsed := c.parse(path)
	title := parsed.Title
	tag := trimTitle(text(meta, "title"))
	if len(tag) > 1 && !strings.Contains(tag, "://") && filepath.Ext(tag) == "" {
		title = tag
	}
	if title == "" {
		title = filepath.Base(path)
	}
	s := &Snapshot{Version: 3, Updated: float64(c.time().UnixNano()) / 1e9, Media: map[string]any{"uri": c.uri, "title": title, "state": status.State, "artist": text(meta, "artist")}, Playback: map[string]any{"position": status.Time, "duration": status.Length, "rate": status.Rate}, Options: activityOptions(config)}
	if parsed.TV {
		s.Media["episode"] = fmt.Sprintf("S%02d · Episode %02d", parsed.Season, parsed.Episode)
	}
	c.clock.apply(s)
	s.ChapterTitle = httpChapter(status.Information.Chapters, status.Information.Chapter)
	return s, parsed, nil
}
func httpChapter(raw []byte, current int) string {
	var labels map[string]string
	if json.Unmarshal(raw, &labels) == nil {
		return meaningfulChapter(labels[fmt.Sprint(current)])
	}
	var values []any
	if json.Unmarshal(raw, &values) == nil && current >= 0 && current < len(values) {
		if s, ok := values[current].(string); ok {
			return meaningfulChapter(s)
		}
	}
	return ""
}
func meaningfulChapter(s string) string {
	s = trimTitle(s)
	lower := strings.ToLower(s)
	if s == "" || len(s) > 120 || strings.HasPrefix(lower, "chapter ") || strings.HasPrefix(lower, "scene ") || strings.Contains(lower, "no chapter description") || strings.Contains(lower, "no scene description") {
		return ""
	}
	if strings.Contains(s, ":") && strings.Trim(s, "0123456789:.,") == "" {
		return ""
	}
	return s
}

// Optional chapter labels when stock VLC's HTTP response only includes chapter numbers.
func probeChapters(ctx context.Context, path string) ([]chapter, bool) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	exe, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, false
	}
	command := exec.CommandContext(ctx, exe, "-v", "quiet", "-show_chapters", "-print_format", "json", path)
	configureChild(command)
	var output limitedBuffer
	output.limit = 1024 * 1024
	command.Stdout = &output
	if command.Run() != nil {
		return nil, false
	}
	var result struct {
		Chapters []chapter `json:"chapters"`
	}
	if json.Unmarshal(output.body, &result) != nil {
		return nil, false
	}
	return result.Chapters, true
}

type chapter struct {
	Start string            `json:"start_time"`
	End   string            `json:"end_time"`
	Tags  map[string]string `json:"tags"`
}
type limitedBuffer struct {
	body  []byte
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.body)+len(p) > b.limit {
		return 0, errors.New("output exceeds limit")
	}
	b.body = append(b.body, p...)
	return len(p), nil
}

func activityOptions(c settings) map[string]string {
	return map[string]string{"large_image": c.LargeImage, "large_text": c.LargeText, "small_image_playing": c.SmallPlaying, "small_image_paused": c.SmallPaused, "small_image_idle": c.SmallIdle}
}

func (c *playbackClient) time() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
func (c *playbackClient) parse(path string) parsedMedia {
	if c.parsedPath != path {
		normalized := strings.ReplaceAll(path, `\`, "/")
		dir := filepath.Dir(normalized)
		if dir != c.directoryPath {
			c.directoryPath, c.directory = dir, parseDirectoryTitle(dir)
		}
		c.parsedPath, c.parsed = path, parseFilenameWithDirectory(normalized, c.directory)
	}
	return c.parsed
}

// Chapter data is independent of TMDb metadata. Share an in-flight probe and keep
// only a bounded set of unchanged files; cancelled probes are never cached.
type chapterEntry struct {
	info          os.FileInfo
	ready         chan struct{}
	labels        []chapter
	expires, used time.Time
}
type chapterCache struct {
	mu      sync.Mutex
	entries map[string]*chapterEntry
	probe   func(context.Context, string) ([]chapter, bool)
	now     func() time.Time
}

func (c *chapterCache) get(ctx context.Context, path string) []chapter {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		now := time.Now()
		if c.now != nil {
			now = c.now()
		}
		c.mu.Lock()
		if c.entries == nil {
			c.entries = map[string]*chapterEntry{}
		}
		e := c.entries[path]
		same := e != nil && os.SameFile(info, e.info) && info.Size() == e.info.Size() && info.ModTime().Equal(e.info.ModTime())
		if same && e.ready != nil {
			ready := e.ready
			c.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return nil
			}
		}
		if same && now.Before(e.expires) {
			e.used = now
			labels := e.labels
			c.mu.Unlock()
			return labels
		}
		if len(c.entries) >= 32 {
			var oldest string
			var at time.Time
			for name, entry := range c.entries {
				if entry.ready == nil && (oldest == "" || entry.used.Before(at)) {
					oldest, at = name, entry.used
				}
			}
			if oldest != "" {
				delete(c.entries, oldest)
			} else {
				c.mu.Unlock()
				return nil
			}
		}
		e = &chapterEntry{info: info, ready: make(chan struct{}), used: now}
		ready := e.ready
		c.entries[path] = e
		c.mu.Unlock()
		probe := c.probe
		if probe == nil {
			probe = probeChapters
		}
		labels, ok := probe(ctx, path)
		c.mu.Lock()
		if ctx.Err() != nil {
			if c.entries[path] == e {
				delete(c.entries, path)
			}
			labels = nil
		} else {
			ttl := 10 * time.Minute
			if ok {
				ttl = 12 * time.Hour
			}
			e.labels = labels
			e.expires = now.Add(ttl)
		}
		e.ready = nil
		close(ready)
		c.mu.Unlock()
		return labels
	}
}
