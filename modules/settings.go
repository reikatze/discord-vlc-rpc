package modules

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type settings struct {
	Enabled             bool     `json:"enabled"`
	ApplicationID       string   `json:"discord_application_id,omitempty"`
	StartupNotification bool     `json:"startup_notification"`
	APIKey              string   `json:"tmdb_api_key"`
	Language            string   `json:"tmdb_language"`
	EpisodeLookup       bool     `json:"tmdb_episode_lookup"`
	CacheDays           int      `json:"tmdb_positive_cache_days"`
	IndexEnabled        bool     `json:"tmdb_local_index"`
	IndexPath           string   `json:"tmdb_index_path"`
	CachePath           string   `json:"metadata_cache_path"`
	PosterFit           string   `json:"poster_fit"`
	Ignored             []string `json:"ignored_paths"`
	LargeImage          string   `json:"large_image"`
	LargeText           string   `json:"large_text"`
	SmallPlaying        string   `json:"small_image_playing"`
	SmallPaused         string   `json:"small_image_paused"`
	SmallIdle           string   `json:"small_image_idle"`
}

func defaults() settings {
	return settings{Enabled: true, StartupNotification: true, Language: "en-US", EpisodeLookup: true, CacheDays: 60, IndexEnabled: true, PosterFit: "contain", LargeText: "VLC", Ignored: []string{}}
}

// The built-in public Discord identifier stays separate from user configuration.
func builtInApplicationID() string { return strconv.FormatUint(0x158b46bfab840078, 10) }
func (c settings) discordApplicationID() string {
	if c.ApplicationID == "" {
		return builtInApplicationID()
	}
	return c.ApplicationID
}
func (c *settings) normalizeApplicationID() {
	c.ApplicationID = strings.TrimSpace(c.ApplicationID)
	if c.ApplicationID == builtInApplicationID() {
		c.ApplicationID = ""
	}
}

// Application data belongs to this user's app configuration folder, independent
// of the VLC installation and selected VLC configuration profile.
func applicationConfigFolder() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "discord-vlc-rpc")
}
func (p paths) settingsFolder() string { return applicationConfigFolder() }
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
	if strings.Trim(c.ApplicationID, "0123456789") != "" {
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
	if p.settingsFolder() == "" {
		return c, errors.New("Cannot determine the application configuration directory")
	}
	err := readJSON(p.settingsFile(), 65536, &c)
	if err != nil {
		return c, err
	}
	c.normalizeApplicationID()
	return c, validateSettings(c)
}
func saveSettings(p paths, c settings) error {
	if p.settingsFolder() == "" {
		return errors.New("Cannot determine the application configuration directory")
	}
	c.normalizeApplicationID()
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
	if p.settingsFolder() == "" {
		return errors.New("Cannot determine the application configuration directory")
	}
	if exists(p.settingsFile()) {
		c := defaults()
		if err := readJSON(p.settingsFile(), 65536, &c); err != nil {
			return err
		}
		originalID := c.ApplicationID
		c.normalizeApplicationID()
		if err := validateSettings(c); err != nil {
			return err
		}
		if c.ApplicationID != originalID {
			return saveSettings(p, c)
		}
		return nil
	}
	return saveSettings(p, defaults())
}
