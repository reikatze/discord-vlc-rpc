package modules

import (
	"math"
	"unicode/utf8"
)

type Snapshot struct {
	Version       int               `json:"version"`
	Updated       float64           `json:"updated"`
	ApplicationID string            `json:"application_id"`
	Media         map[string]any    `json:"media"`
	Options       map[string]string `json:"options"`
	Playback      map[string]any    `json:"playback"`
	Timestamps    map[string]int64  `json:"-"`
	ChapterTitle  string            `json:"chapter_title"`
}

func text(m map[string]any, key string) string    { s, _ := m[key].(string); return s }
func number(m map[string]any, key string) float64 { n, _ := m[key].(float64); return n }
func bounded(s string) string {
	if len(s) <= 120 {
		return s
	}
	s = s[:120]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func makeActivity(s *Snapshot) map[string]any {
	if s == nil || s.Version != 3 || s.Media == nil {
		return nil
	}
	media, playback, options := s.Media, s.Playback, s.Options
	title := bounded(text(media, "title"))
	state := "Playing"
	switch text(media, "state") {
	case "paused":
		state = "Paused"
	case "buffering":
		state = "Buffering"
	case "idle":
		state = "Idle"
	}
	chapter := first(s.ChapterTitle, text(media, "chapter_title"))
	episode := first(text(playback, "chapter_base"), text(media, "episode"))
	eligible, exists := playback["chapter_eligible"].(bool)
	if chapter != "" && (!exists || eligible) {
		if episode != "" {
			episode += ": " + chapter
		} else {
			episode = chapter
		}
	}
	second := first(episode, text(media, "subtitle"), text(media, "artist"), state)
	if state != "Playing" {
		second = state
	}
	activity := map[string]any{"type": 3, "name": title, "status_display_type": 2, "details": title, "state": bounded(second)}
	assets := map[string]any{}
	poster := first(text(media, "poster"), options["large_image"])
	if poster != "" {
		largeText := bounded(options["large_text"])
		if text(media, "poster") != "" {
			largeText = title
		}
		assets["large_image"] = poster
		assets["large_text"] = largeText
	}
	badge := options["small_image_playing"]
	if state == "Idle" {
		badge = options["small_image_idle"]
	} else if state == "Paused" || state == "Buffering" {
		badge = options["small_image_paused"]
	}
	if badge != "" {
		assets["small_image"] = badge
		assets["small_text"] = state
	}
	if url := text(media, "url"); url != "" {
		activity["details_url"] = url
		activity["state_url"] = first(text(media, "episode_url"), url)
		if poster != "" {
			assets["large_url"] = url
		}
	}
	if len(assets) > 0 {
		activity["assets"] = assets
	}
	position, duration, rate := number(playback, "position"), number(playback, "duration"), number(playback, "rate")
	if state == "Playing" && rate > 0 && position >= 0 && position <= duration && duration > 0 && finite(position) && finite(duration) && finite(rate) && finite(s.Updated) {
		if s.Timestamps != nil {
			activity["timestamps"] = s.Timestamps
		} else {
			activity["timestamps"] = map[string]int64{"start": int64(math.Floor(s.Updated - position/rate)), "end": int64(math.Floor(s.Updated + (duration-position)/rate))}
		}
	}
	return activity
}
func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// VLC reports whole-second positions. Hold the clock through sampling jitter,
// and re-anchor on media, playback-state, duration/rate changes or detectable seeks.
type playbackClock struct {
	uri                          string
	at, position, rate, duration float64
	stamps                       map[string]int64
}

func (c *playbackClock) apply(s *Snapshot) {
	s.Timestamps = nil
	position, duration, rate := number(s.Playback, "position"), number(s.Playback, "duration"), number(s.Playback, "rate")
	if text(s.Media, "state") != "playing" || rate <= 0 || duration <= 0 || position < 0 || position > duration || !finite(position) || !finite(duration) || !finite(rate) || !finite(s.Updated) {
		*c = playbackClock{}
		return
	}
	uri := text(s.Media, "uri")
	predicted := c.position + (s.Updated-c.at)*c.rate
	if c.stamps == nil || c.uri != uri || c.rate != rate || c.duration != duration || s.Updated < c.at || math.Abs(position-predicted) > 1.5 {
		c.uri, c.at, c.position, c.rate, c.duration = uri, s.Updated, position, rate, duration
		c.stamps = map[string]int64{"start": int64(math.Floor(s.Updated - position/rate)), "end": int64(math.Floor(s.Updated + (duration-position)/rate))}
	}
	s.Timestamps = c.stamps
}
