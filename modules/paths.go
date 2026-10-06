package modules

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type paths struct {
	config, vlc, configFile                         string
	vlcExplicit, configExplicit, configFileExplicit bool
}

func defaultPaths() paths {
	home, _ := os.UserHomeDir()
	p := paths{}
	switch runtime.GOOS {
	case "windows":
		p.config = filepath.Join(os.Getenv("APPDATA"), "vlc")
	case "darwin":
		p.config = filepath.Join(home, "Library/Preferences/org.videolan.vlc")
		for _, v := range []string{filepath.Join(home, "Applications/VLC.app"), "/Applications/VLC.app"} {
			if exists(v) {
				p.vlc = v
				break
			}
		}
	default:
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		p.config = filepath.Join(base, "vlc")
		if exists("/usr/share/vlc") {
			p.vlc = "/usr/share/vlc"
		}
	}
	if runtime.GOOS == "windows" {
		p.vlc = findVLC()
	}
	return p
}
func (p paths) vlcFolder() string {
	if !p.vlcExplicit {
		pinned := p
		pinned.configExplicit, pinned.configFileExplicit = true, true
		pinned.configFile = p.vlcConfigFile()
		if found, err := discoverPaths(pinned); err == nil && found.vlc != "" {
			return found.vlc
		}
	}
	return p.vlc
}
func openFolder(path string) error {
	if path == "" {
		return fmt.Errorf("VLC folder not found; start with --vlc-dir pointing to your VLC installation")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	name := "xdg-open"
	args := []string{path}
	if runtime.GOOS == "windows" {
		name = "explorer.exe"
	} else if runtime.GOOS == "darwin" {
		name = "open"
		if strings.HasSuffix(path, ".app") {
			path = filepath.Join(path, "Contents")
		}
		args = []string{path}
	}
	cmd := exec.Command(name, args...)
	configureChild(cmd)
	return cmd.Run()
}

func isDirectory(path string) bool { info, err := os.Stat(path); return err == nil && info.IsDir() }
