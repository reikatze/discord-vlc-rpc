package modules

import (
	"context"
	"flag"
	"fmt"
	"github.com/gogpu/systray"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// Run starts the application. Call it once from the executable entry point.
func Run(trayIcon []byte) {
	runtime.LockOSThread()
	p := defaultPaths()
	flag.StringVar(&p.config, "config-dir", p.config, "VLC configuration directory")
	flag.StringVar(&p.vlc, "vlc-dir", p.vlc, "VLC installation folder")
	flag.StringVar(&p.configFile, "config-file", "", "VLC configuration file (overrides profile discovery)")
	headless := flag.Bool("headless", false, "run without a tray icon")
	flag.Parse()
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "vlc-dir":
			p.vlcExplicit = true
		case "config-dir":
			p.configExplicit = true
		case "config-file":
			p.configFileExplicit = true
		}
	})
	p.config, _ = filepath.Abs(p.config)
	if p.vlc != "" {
		p.vlc, _ = filepath.Abs(p.vlc)
	}
	if p.configFileExplicit {
		p.configFile, _ = filepath.Abs(p.configFile)
		if !p.configExplicit {
			p.config = filepath.Dir(p.configFile)
		}
	}
	resolved, err := discoverPaths(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	p = resolved
	lock, e := acquireInstanceLock(p.settingsFolder())
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return
	}
	defer lock.Close()
	if err := initSettings(p); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go p.trackVLCFolder(ctx)
	s := &service{paths: p, status: "Starting", refresh: make(chan struct{}, 1), buildIndex: make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() { done <- s.run(ctx); cancel() }()
	if *headless {
		e = <-done
	} else {
		tray := systray.New()
		menu := systray.NewMenu()
		notify := func(err error) {
			if err != nil {
				tray.ShowNotification("discord-vlc-rpc", err.Error())
			}
		}
		exe, _ := os.Executable()
		if err := installDesktopIcon(append([]string{exe}, os.Args[1:]...)); err != nil {
			fmt.Fprintln(os.Stderr, "Application icon:", err)
		}
		args := []string{exe, "--config-dir", p.config}
		if p.configFile != "" && p.configFile != filepath.Join(p.config, "vlcrc") {
			args = append(args, "--config-file", p.configFile)
		}
		if p.vlc != "" {
			args = append(args, "--vlc-dir", p.vlc)
		}
		var auto *systray.MenuItem
		auto = menu.AddCheckbox("Autostart", autostartEnabled(), func() { notify(setAutostart(!auto.IsChecked(), args)); auto.SetChecked(autostartEnabled()) })
		var enabled *systray.MenuItem
		c, _ := loadSettings(p)
		enabled = menu.AddCheckbox("Enable Discord presence", c.Enabled, func() {
			c, err := loadSettings(p)
			if err != nil {
				notify(err)
				return
			}
			c.Enabled = !c.Enabled
			notify(saveSettings(p, c))
			poke(s.refresh)
		})
		var sessionPresence *systray.MenuItem
		sessionPresence = menu.AddCheckbox("Pause presence for this session", false, func() { s.mu.Lock(); s.noPresence = !s.noPresence; s.mu.Unlock(); poke(s.refresh) })
		menu.Add("Refresh playback / metadata", func() { poke(s.refresh) })
		httpStatus := menu.Add("VLC HTTP: Not checked", nil)
		httpStatus.SetDisabled(true)
		var httpSetup *systray.MenuItem
		httpSetup = menu.Add("Check / Configure VLC HTTP Interface", func() {
			httpSetup.SetDisabled(true)
			httpStatus.SetLabel("VLC HTTP: Checking")
			go func() {
				defer httpSetup.SetDisabled(false)
				message, err := checkConfigureHTTP(ctx, p, func(message string) {
					httpStatus.SetLabel("VLC HTTP: Waiting for VLC to close")
					tray.ShowNotification("VLC HTTP setup", message)
				})
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					httpStatus.SetLabel("VLC HTTP: Check failed")
					notify(err)
				} else {
					label := "VLC HTTP: Configured — start/restart VLC"
					if message == "VLC HTTP interface is responding." {
						label = "VLC HTTP: Ready"
					}
					httpStatus.SetLabel(label)
					tray.ShowNotification("VLC HTTP setup", message)
					poke(s.refresh)
				}
			}()
		})
		rpc := menu.Add("Discord Rich Presence: Starting", nil)
		rpc.SetDisabled(true)
		playback := menu.Add("Playback: Waiting for VLC", nil)
		playback.SetDisabled(true)
		metadata := menu.Add("TMDb: Filename only", nil)
		metadata.SetDisabled(true)
		index := menu.Add("Database: Waiting", nil)
		index.SetDisabled(true)
		menu.AddSeparator()
		var sessionIndex *systray.MenuItem
		sessionIndex = menu.AddCheckbox("Pause database updates for this session", false, func() { s.mu.Lock(); s.noIndex = !s.noIndex; s.mu.Unlock(); poke(s.refresh) })
		menu.Add("Build / refresh local title database", func() { poke(s.buildIndex) })
		menu.Add("Clear metadata cache", func() {
			go func() {
				c, err := loadSettings(p)
				if err == nil {
					err = clearMetadataCache(p.cacheFolder(c))
				}
				notify(err)
				poke(s.refresh)
			}()
		})
		menu.Add("Open VLC folder", func() { go func() { notify(openFolder(p.vlcFolder())) }() })
		menu.Add("Open database folder", func() { go func() { c, _ := loadSettings(p); notify(openFolder(p.indexFolder(c))) }() })
		menu.Add("Open configuration folder", func() { go func() { notify(openFolder(p.settingsFolder())) }() })
		menu.Add("Open metadata cache folder", func() { go func() { c, _ := loadSettings(p); notify(openFolder(p.cacheFolder(c))) }() })
		menu.AddSeparator()
		menu.Add("Quit", cancel)
		tray.SetIcon(trayIcon).SetAppName("discord-vlc-rpc").SetTooltip("discord-vlc-rpc").SetMenu(menu).Show()
		if c.StartupNotification {
			tray.ShowNotification("discord-vlc-rpc", "discord-vlc-rpc has started and is running in the system tray.")
		}
		go func() {
			settingsCache := cachedFile[settings]{path: s.paths.settingsFile(), load: func() (settings, error) { return loadSettings(s.paths) }}
			labels := map[*systray.MenuItem]string{rpc: "Discord Rich Presence: Starting", playback: "Playback: Waiting for VLC", metadata: "TMDb: Filename only", index: "Database: Waiting"}
			setLabel := func(item *systray.MenuItem, value string) {
				if labels[item] != value {
					labels[item] = value
					item.SetLabel(value)
				}
			}
			setChecked := func(item *systray.MenuItem, value bool) {
				if item.IsChecked() != value {
					item.SetChecked(value)
				}
			}
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					tray.Remove()
					return
				case <-ticker.C:
					setLabel(rpc, "Discord Rich Presence: "+s.getStatus())
					p, m, i, noPresence, noIndex := s.details()
					setLabel(playback, "Playback: "+bounded(p))
					setLabel(metadata, "TMDb: "+bounded(m))
					setLabel(index, "Database: "+bounded(i))
					setChecked(sessionPresence, noPresence)
					setChecked(sessionIndex, noIndex)
					c, err, _ := settingsCache.read(false)
					if err == nil {
						setChecked(enabled, c.Enabled)
					}
				}
			}
		}()
		e = tray.Run()
		cancel()
		serviceErr := <-done
		if e == nil {
			e = serviceErr
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
