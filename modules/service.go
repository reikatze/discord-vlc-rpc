package modules

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type service struct {
	rpcDial                           func() (net.Conn, error)
	lookup                            func(context.Context, paths, settings, parsedMedia, string) (*metadataHit, []chapter, error)
	paths                             paths
	mu                                sync.RWMutex
	status, playback, index, metadata string
	noPresence, noIndex               bool
	refresh, buildIndex               chan struct{}
}

func (s *service) setStatus(v string) { s.mu.Lock(); s.status = v; s.mu.Unlock() }
func (s *service) getStatus() string  { s.mu.RLock(); defer s.mu.RUnlock(); return s.status }
func (s *service) details() (string, string, string, bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.playback, s.metadata, s.index, s.noPresence, s.noIndex
}
func (s *service) state(playback, metadata, index string) {
	s.mu.Lock()
	if playback != "" {
		s.playback = playback
	}
	if metadata != "" {
		s.metadata = metadata
	}
	if index != "" {
		s.index = index
	}
	s.mu.Unlock()
}
func poke(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

type desired struct {
	app      string
	activity map[string]any
}
type lookupResult struct {
	key      string
	id       uint64
	hit      *metadataHit
	chapters []chapter
	err      error
}

func (s *service) run(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	settingsCache := cachedFile[settings]{path: s.paths.settingsFile(), load: func() (settings, error) { return loadSettings(s.paths) }}
	vlcCache := cachedFile[map[string]string]{path: s.paths.vlcConfigFile(), load: func() (map[string]string, error) {
		body, err := readVLCConfig(s.paths.vlcConfigFile())
		return vlcOptions(body), err
	}}
	c, err, _ := settingsCache.read(false)
	if err != nil {
		return err
	}
	updates := make(chan desired, 1)
	done := make(chan struct{})
	initialApp := c.ApplicationID
	go func() {
		defer close(done)
		r := &discordRPC{dial: s.rpcDial}
		defer r.close()
		d := desired{app: initialApp}
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				if r.connection != nil {
					_ = r.send(1, map[string]any{"cmd": "SET_ACTIVITY", "args": map[string]any{"pid": os.Getpid(), "activity": nil}, "nonce": "shutdown"})
				}
				return
			case d = <-updates:
			case <-tick.C:
			}
			r.update(d.app, d.activity)
			s.setStatus(r.status)
		}
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
		}
	}()
	publish := func(activity map[string]any) {
		d := desired{c.ApplicationID, activity}
		select {
		case updates <- d:
		default:
			select {
			case <-updates:
			default:
			}
			updates <- d
		}
	}
	client := &playbackClient{}
	chapterLabels := &chapterCache{}
	results := make(chan lookupResult, 2)
	indexDone := make(chan error, 1)
	var lookupCancel, indexCancel context.CancelFunc
	var lookupID uint64
	cancelLookup := func() {
		lookupID++
		if lookupCancel != nil {
			lookupCancel()
			lookupCancel = nil
		}
	}
	defer func() {
		cancelLookup()
		if indexCancel != nil {
			indexCancel()
		}
	}()
	key, lastConfig, lookupStatus, indexScope := "", "", "Filename only", ""
	var hit *metadataHit
	var chapters []chapter
	nextLookup, nextIndex := time.Time{}, time.Time{}
	configHash := ""
	indexRunning := false
	forceIndex := false
	poll := vlcPollBackoff{}
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	for {
		timer.Reset(poll.interval())
		forceReload := false
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		case <-s.refresh:
			forceReload = true
			poll.success()
			cancelLookup()
			key = ""
		case <-s.buildIndex:
			nextIndex = time.Time{}
			forceIndex = true
		}
		if latest, e, changed := settingsCache.read(forceReload); e == nil {
			c = latest
			if changed || configHash == "" {
				body, _ := json.Marshal(c)
				configHash = string(body)
			}
		} else {
			s.state("", "Invalid configuration; keeping last valid settings", "")
		}
		_, _, _, noPresence, noIndex := s.details()
		if configHash != lastConfig {
			cancelLookup()
			key = ""
			lastConfig = configHash
		}
		activeIndex := c.IndexEnabled && !noIndex && c.APIKey != ""
		scope := s.paths.indexFolder(c)
		if (!activeIndex || scope != indexScope) && indexRunning {
			indexCancel()
			indexScope = ""
		}
		select {
		case e := <-indexDone:
			indexRunning = false
			nextIndex = time.Now().Add(30 * time.Minute)
			if e != nil {
				s.state("", "", "Update failed; will retry")
			} else {
				s.state("", "", indexDescription(scope))
				key = ""
			}
		default:
		}
		if !activeIndex {
			s.state("", "", "Disabled or waiting for TMDb key")
		} else if !indexRunning && time.Now().After(nextIndex) {
			m, e := readManifest(scope)
			want := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02")
			if e != nil || m.Date != want || forceIndex {
				forceIndex = false
				indexCtx, stop := context.WithTimeout(ctx, 15*time.Minute)
				indexCancel = stop
				indexScope = scope
				indexRunning = true
				s.state("", "", "Building in background")
				go func() {
					e := refreshIndex(indexCtx, scope, downloadExport)
					stop()
					select {
					case indexDone <- e:
					case <-ctx.Done():
					}
				}()
			} else {
				nextIndex = time.Now().Add(time.Hour)
				s.state("", "", indexDescription(scope))
			}
		}
		if !c.Enabled || noPresence {
			client.clock = playbackClock{}
			poll.delay = 5 * time.Second
			cancelLookup()
			key = ""
			hit = nil
			publish(nil)
			s.state("Presence disabled", "", "")
			continue
		}
		options, e, _ := vlcCache.read(forceReload)
		if e != nil {
			client.clock = playbackClock{}
			cancelLookup()
			key = ""
			hit = nil
			publish(nil)
			poll.failed()
			s.state("Cannot read VLC settings", "", "")
			continue
		}
		snapshot, parsed, e := client.sample(ctx, options, c)
		if e != nil {
			client.clock = playbackClock{}
			cancelLookup()
			key = ""
			hit = nil
			publish(nil)
			poll.failed()
			s.state(e.Error(), "", "")
			continue
		}
		poll.success()
		if snapshot == nil {
			cancelLookup()
			key = ""
			hit = nil
			publish(nil)
			s.state("Stopped, ignored or network media", "", "")
			continue
		}
		uri := text(snapshot.Media, "uri")
		if uri == "" {
			cancelLookup()
			key = ""
			hit = nil
			chapters = nil
			publish(makeActivity(snapshot))
			s.state("Idle", "Filename only", "")
			continue
		}
		currentKey := uri + "\x00" + cacheKey(parsed, c) + "\x00" + configHash
		if currentKey != key {
			cancelLookup()
			key = currentKey
			hit = nil
			chapters = nil
			nextLookup = time.Time{}
			lookupStatus = "Filename only"
		}
		for {
			select {
			case result := <-results:
				if result.key == key && result.id == lookupID {
					lookupCancel = nil
					chapters = result.chapters
					if result.err != nil {
						lookupStatus = result.err.Error()
						nextLookup = time.Now().Add(time.Minute)
					} else {
						hit = result.hit
						lookupStatus = "No TMDb match"
						if c.APIKey == "" {
							lookupStatus = "Filename only"
						}
						if hit != nil {
							lookupStatus = "TMDb matched"
						}
						nextLookup = time.Now().Add(time.Duration(c.CacheDays) * 24 * time.Hour)
						if hit == nil {
							nextLookup = time.Now().Add(time.Hour)
						} else if !hit.Complete {
							nextLookup = time.Now().Add(10 * time.Minute)
						}
					}
				}
			default:
				goto drained
			}
		}
	drained:
		if lookupCancel == nil && time.Now().After(nextLookup) {
			lookupCtx, stop := context.WithTimeout(ctx, 90*time.Second)
			lookupCancel = stop
			lookupID++
			requestID := lookupID
			tag := key
			cfg := c
			cfg.IndexEnabled = activeIndex
			p := s.paths
			path, _ := localMediaPath(uri)
			lookupStatus = "Looking up metadata"
			go func() {
				defer stop()
				lookup := s.lookup
				if lookup == nil {
					lookup = func(ctx context.Context, p paths, c settings, parsed parsedMedia, path string) (*metadataHit, []chapter, error) {
						h, err := newTMDB(p, c).lookup(ctx, parsed)
						return h, chapterLabels.get(ctx, path), err
					}
				}
				h, labels, e := lookup(lookupCtx, p, cfg, parsed, path)
				result := lookupResult{key: tag, id: requestID, hit: h, chapters: labels, err: e}
				select {
				case results <- result:
				case <-ctx.Done():
				}
			}()
		}
		if snapshot.ChapterTitle == "" {
			position := number(snapshot.Playback, "position")
			for _, chapter := range chapters {
				start, _ := strconv.ParseFloat(chapter.Start, 64)
				end, _ := strconv.ParseFloat(chapter.End, 64)
				if position >= start && position < end {
					snapshot.ChapterTitle = meaningfulChapter(chapter.Tags["title"])
					break
				}
			}
		}
		enrich(snapshot, hit)
		publish(makeActivity(snapshot))
		s.state(strings.Title(text(snapshot.Media, "state"))+": "+text(snapshot.Media, "title"), lookupStatus, "")
	}
}

type vlcPollBackoff struct{ delay time.Duration }

func (p *vlcPollBackoff) interval() time.Duration {
	if p.delay == 0 {
		return 500 * time.Millisecond
	}
	return p.delay
}
func (p *vlcPollBackoff) success() { p.delay = 0 }
func (p *vlcPollBackoff) failed() {
	p.delay = p.interval() * 2
	if p.delay > 5*time.Second {
		p.delay = 5 * time.Second
	}
}
