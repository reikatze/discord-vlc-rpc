package modules

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

type settings struct {
	Enabled       bool     `json:"enabled"`
	ApplicationID string   `json:"discord_application_id"`
	APIKey        string   `json:"tmdb_api_key"`
	Language      string   `json:"tmdb_language"`
	EpisodeLookup bool     `json:"tmdb_episode_lookup"`
	CacheDays     int      `json:"tmdb_positive_cache_days"`
	IndexEnabled  bool     `json:"tmdb_local_index"`
	IndexPath     string   `json:"tmdb_index_path"`
	CachePath     string   `json:"metadata_cache_path"`
	PosterFit     string   `json:"poster_fit"`
	Ignored       []string `json:"ignored_paths"`
	LargeImage    string   `json:"large_image"`
	LargeText     string   `json:"large_text"`
	SmallPlaying  string   `json:"small_image_playing"`
	SmallPaused   string   `json:"small_image_paused"`
	SmallIdle     string   `json:"small_image_idle"`
}

func defaults() settings {
	return settings{Enabled: true, ApplicationID: "1552412285589520504", Language: "en-US", EpisodeLookup: true, CacheDays: 60, IndexEnabled: true, PosterFit: "contain", LargeText: "VLC", Ignored: []string{}}
}
func (p paths) settingsFolder() string { return filepath.Join(p.config, "discord-vlc-rpc") }
func (p paths) settingsFile() string   { return filepath.Join(p.settingsFolder(), "config.json") }
func (p paths) indexFolder(c settings) string {
	if c.IndexPath != "" {
		return c.IndexPath
	}
	return filepath.Join(p.settingsFolder(), "tmdb-index")
}
func (p paths) cacheFolder(c settings) string {
	if c.CachePath != "" {
		return c.CachePath
	}
	return filepath.Join(p.settingsFolder(), "metadata-cache")
}
func validateSettings(c settings) error {
	if c.ApplicationID == "" || strings.Trim(c.ApplicationID, "0123456789") != "" {
		return errors.New("Discord application ID must contain digits")
	}
	if c.CacheDays < 1 || c.CacheDays > 3650 {
		return errors.New("Cache lifetime must be between 1 and 3650 days")
	}
	if c.PosterFit != "raw" && c.PosterFit != "contain" {
		return errors.New("Poster fit must be raw or contain")
	}
	if c.Language == "" || len(c.Language) > 20 {
		return errors.New("TMDb language is invalid")
	}
	for _, path := range []string{c.IndexPath, c.CachePath} {
		if path != "" && !filepath.IsAbs(path) {
			return errors.New("Database and cache paths must be absolute")
		}
	}
	return nil
}
func loadSettings(p paths) (settings, error) {
	c := defaults()
	err := readJSON(p.settingsFile(), 65536, &c)
	if err != nil {
		return c, err
	}
	return c, validateSettings(c)
}
func saveSettings(p paths, c settings) error {
	if err := validateSettings(c); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(p.settingsFile(), append(b, '\n'), 0600)
}
func initSettings(p paths) error {
	if exists(p.settingsFile()) {
		_, err := loadSettings(p)
		return err
	}
	return saveSettings(p, defaults())
}
