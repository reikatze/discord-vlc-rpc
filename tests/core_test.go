package modules

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func exampleSnapshot() *Snapshot {
	return &Snapshot{Version: 3, Updated: 100, ApplicationID: "123", Media: map[string]any{"title": "Movie", "state": "playing"}, Playback: map[string]any{"position": float64(20), "duration": float64(100), "rate": float64(2)}}
}
func TestActivityTimestampsPausedAndUTF8(t *testing.T) {
	s := exampleSnapshot()
	a := makeActivity(s)
	if !reflect.DeepEqual(a["timestamps"], map[string]int64{"start": 90, "end": 140}) {
		t.Fatal(a)
	}
	s.Media["state"] = "paused"
	a = makeActivity(s)
	if a["timestamps"] != nil || a["state"] != "Paused" {
		t.Fatal(a)
	}
}
func TestAtomicReplaceFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, []byte("old"), 0600)
	if replaceFile(file+"missing", file) == nil {
		t.Fatal("expected failure")
	}
	b, _ := os.ReadFile(file)
	if string(b) != "old" {
		t.Fatal("old file lost")
	}
	if err := atomicWrite(file, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestFilenameFormsAndPrivacy(t *testing.T) {
	for _, test := range []struct {
		path, title, year string
		season, episode   int
	}{{"Movie.2024.1080p.BluRay.x264.mkv", "Movie", "2024", 0, 0}, {"Show.S02E05.1080p.mkv", "Show", "", 2, 5}, {"Show.2x05.mkv", "Show", "", 2, 5}, {"Show S2 - 03.mkv", "Show", "", 2, 3}, {"[SubsPlease] Anime - 05v2 [1080p].mkv", "Anime", "", 1, 5}, {"1917.2019.mkv", "1917", "2019", 0, 0}, {"The.Movie.(Extended.Edition).2024.mkv", "The Movie (Extended Edition)", "2024", 0, 0}} {
		p := parseFilename(test.path)
		if p.Title != test.title || p.Year != test.year || p.Season != test.season || p.Episode != test.episode {
			t.Errorf("%s => %+v", test.path, p)
		}
	}
	if !ignoredPath(`C:\Media\Private\movie.mkv`, []string{`c:\media\private`}) || ignoredPath(`/media/private-other/a`, []string{`/media/private`}) {
		t.Fatal("ignored path boundary")
	}
	if _, ok := localMediaPath("https://example.test/video"); ok {
		t.Fatal("network treated as local")
	}
}
func TestSettingsDefaultsPersistenceAndValidation(t *testing.T) {
	p := testPaths(t)
	if err := initSettings(p); err != nil {
		t.Fatal(err)
	}
	c, err := loadSettings(p)
	if err != nil || !reflect.DeepEqual(c, defaults()) {
		t.Fatal("defaults", err)
	}
	c.Enabled = false
	c.APIKey = "private"
	c.Ignored = []string{"/private"}
	if err := saveSettings(p, c); err != nil {
		t.Fatal(err)
	}
	if err := initSettings(p); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSettings(p)
	if err != nil || !reflect.DeepEqual(loaded, c) {
		t.Fatal("settings not preserved", err)
	}
	c.CacheDays = 0
	if saveSettings(p, c) == nil {
		t.Fatal("invalid config saved")
	}
}

func TestBuiltInIDAndStartupNotificationSettings(t *testing.T) {
	p := testPaths(t)
	// A configuration without the new preference keeps notifications enabled.
	legacy, err := json.Marshal(map[string]any{"discord_application_id": builtInApplicationID(), "tmdb_api_key": "keep-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(p.settingsFile(), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if err := initSettings(p); err != nil {
		t.Fatal(err)
	}
	c, err := loadSettings(p)
	if err != nil || c.ApplicationID != "" || c.discordApplicationID() == "" || !c.StartupNotification || c.APIKey != "keep-key" {
		t.Fatal("built-in defaults or migration", c, err)
	}
	body, err := os.ReadFile(p.settingsFile())
	if err != nil || strings.Contains(string(body), builtInApplicationID()) || strings.Contains(string(body), "discord_application_id") {
		t.Fatal("built-in ID persisted", err)
	}
	c.ApplicationID, c.StartupNotification = "123", false
	if err := saveSettings(p, c); err != nil {
		t.Fatal(err)
	}
	c, err = loadSettings(p)
	if err != nil || c.discordApplicationID() != "123" || c.StartupNotification {
		t.Fatal("custom settings lost", c, err)
	}
	c.ApplicationID = "invalid-id"
	if saveSettings(p, c) == nil {
		t.Fatal("invalid application ID accepted")
	}
}
func TestServiceHeadlessShutdown(t *testing.T) {
	p := testPaths(t)
	if err := initSettings(p); err != nil {
		t.Fatal(err)
	}
	s := &service{paths: p, refresh: make(chan struct{}, 1), buildIndex: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown unbounded")
	}
	c, _ := loadSettings(p)
	b, _ := json.Marshal(c)
	if len(b) == 0 {
		t.Fatal("no standalone config")
	}
}

func TestConfigurationCacheChangesErrorsAndForcedReload(t *testing.T) {
	p := testPaths(t)
	if err := initSettings(p); err != nil {
		t.Fatal(err)
	}
	reads := 0
	cache := cachedFile[settings]{path: p.settingsFile(), load: func() (settings, error) { reads++; return loadSettings(p) }}
	original, err, _ := cache.read(false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err, changed := cache.read(false); err != nil || changed {
			t.Fatal("unchanged config reloaded", err)
		}
	}
	if reads != 1 {
		t.Fatal("unnecessary reads", reads)
	}
	// Atomic replacement with identical size and modification time must reload.
	info, _ := os.Stat(p.settingsFile())
	body, _ := os.ReadFile(p.settingsFile())
	replacement := p.settingsFile() + ".replacement"
	if err := os.WriteFile(replacement, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(replacement, p.settingsFile()); err != nil {
		t.Fatal(err)
	}
	if _, err, changed := cache.read(false); err != nil || !changed {
		t.Fatal("replacement missed", err)
	}
	// Invalid edits report an error repeatedly without reparsing.
	if err := atomicWrite(p.settingsFile(), []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err, changed := cache.read(false); err == nil || !changed {
		t.Fatal("invalid edit missed")
	}
	count := reads
	if _, err, changed := cache.read(false); err == nil || changed || reads != count {
		t.Fatal("invalid config reread")
	}
	original.Enabled = false
	if err := saveSettings(p, original); err != nil {
		t.Fatal(err)
	}
	if c, err, changed := cache.read(false); err != nil || !changed || c.Enabled {
		t.Fatal("valid edit not reloaded", err)
	}
	if err := os.Remove(p.settingsFile()); err != nil {
		t.Fatal(err)
	}
	if _, err, _ := cache.read(false); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("deletion missed", err)
	}
	if err := saveSettings(p, original); err != nil {
		t.Fatal(err)
	}
	if _, err, changed := cache.read(false); err != nil || !changed {
		t.Fatal("recreation missed", err)
	}
	count = reads
	if _, err, changed := cache.read(true); err != nil || !changed || reads != count+1 {
		t.Fatal("forced reload ignored", err)
	}
}

func TestVLCConfigurationCacheMissingCreationAndEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vlcrc")
	reads := 0
	cache := cachedFile[map[string]string]{path: path, load: func() (map[string]string, error) { reads++; b, e := readVLCConfig(path); return vlcOptions(b), e }}
	if v, err, _ := cache.read(false); err != nil || len(v) != 0 {
		t.Fatal(v, err)
	}
	cache.read(false)
	if reads != 1 {
		t.Fatal("missing file reread")
	}
	for _, password := range []string{"first", "second"} {
		if err := atomicWrite(path, []byte("http-password="+password+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if v, err, changed := cache.read(false); err != nil || !changed || v["http-password"] != password {
			t.Fatal("VLC edit missed", v, err)
		}
	}
}

func BenchmarkVLCConfigurationPolling(b *testing.B) {
	path := filepath.Join(b.TempDir(), "vlcrc")
	if err := os.WriteFile(path, []byte(strings.Repeat("# VLC preference description\n#option=default\n", 3000)+"http-password=example\n"), 0600); err != nil {
		b.Fatal(err)
	}
	load := func() (map[string]string, error) { body, err := readVLCConfig(path); return vlcOptions(body), err }
	b.Run("uncached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := load(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cached", func(b *testing.B) {
		cache := cachedFile[map[string]string]{path: path, load: load}
		cache.read(false)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err, _ := cache.read(false); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Isolate the user's application folder and VLC profile for every settings test.
func testPaths(t *testing.T) paths {
	t.Helper()
	root := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", root)
	case "darwin":
		t.Setenv("HOME", root)
	default:
		t.Setenv("XDG_CONFIG_HOME", root)
	}
	return paths{config: filepath.Join(root, "vlc")}
}

func TestApplicationStorageIndependentOfVLCProfile(t *testing.T) {
	p := testPaths(t)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(base, "discord-vlc-rpc")
	if p.settingsFolder() != app || p.settingsFile() != filepath.Join(app, "config.json") {
		t.Fatal("incorrect standalone settings location", p.settingsFile())
	}
	if vlcFolderStateFile() != filepath.Join(app, "last-vlc.json") {
		t.Fatal("remembered folder is not alongside settings")
	}
	icon, err := desktopIconPath()
	if err != nil || icon != filepath.Join(app, "icon.png") {
		t.Fatal("icon location differs", icon, err)
	}
	vlcFile := p.vlcConfigFile()
	original := []byte("http-password=vlc-private\n")
	if err := atomicWrite(vlcFile, original, 0600); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(p.config, "discord-vlc-rpc", "config.json")
	if err := atomicWrite(old, []byte(`{"enabled":false,"tmdb_api_key":"obsolete"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := initSettings(p); err != nil {
		t.Fatal(err)
	}
	c, err := loadSettings(p)
	if err != nil || !reflect.DeepEqual(c, defaults()) {
		t.Fatal("fresh settings not created", c, err)
	}
	if p.indexFolder(c) != filepath.Join(app, "tmdb-index") || p.cacheFolder(c) != filepath.Join(app, "metadata-cache") {
		t.Fatal("generated data still follows VLC profile")
	}
	other := paths{config: filepath.Join(t.TempDir(), "portable"), configExplicit: true, configFile: filepath.Join(t.TempDir(), "custom-vlcrc"), configFileExplicit: true}
	if other.settingsFile() != p.settingsFile() || other.indexFolder(c) != p.indexFolder(c) || other.cacheFolder(c) != p.cacheFolder(c) {
		t.Fatal("VLC overrides relocate application data")
	}
	c.Enabled = false
	if err := saveSettings(other, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSettings(p)
	if err != nil || loaded.Enabled {
		t.Fatal("application settings not shared across profiles", err)
	}
	c.IndexPath = filepath.Join(t.TempDir(), "custom-index")
	c.CachePath = filepath.Join(t.TempDir(), "custom-cache")
	if p.indexFolder(c) != c.IndexPath || p.cacheFolder(c) != c.CachePath {
		t.Fatal("explicit data overrides lost")
	}
	body, err := os.ReadFile(vlcFile)
	if err != nil || string(body) != string(original) {
		t.Fatal("VLC configuration changed", err)
	}
	body, err = os.ReadFile(old)
	if err != nil || string(body) != `{"enabled":false,"tmdb_api_key":"obsolete"}` {
		t.Fatal("old app configuration was altered", err)
	}
}
